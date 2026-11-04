package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

var ErrConversationBusy = errors.New("conversation is busy")

// ConversationAccess describes storage access, without interpreting the turn.
type ConversationAccess struct {
	CreateConversation bool
	Wait               bool
}

// ConversationStore enforces ownership and grants exclusive access. Unknown IDs
// cannot be opened without CreateConversation. Waiting must honor cancellation.
type ConversationStore interface {
	Acquire(context.Context, uuid.UUID, uuid.UUID, ConversationAccess) (LockedConversation, error)
}

// LockedConversation owns exclusive access until Release. Snapshot and Save copy
// state; local changes never write implicitly. Each successful Save is durable for
// this process even if later processing fails. Save must reject cancelled contexts.
// A newly created conversation with no successful Save is removed on Release.
type LockedConversation interface {
	Snapshot() Conversation
	Save(context.Context, Conversation) error
	Release()
}

type memoryConversation struct {
	patientID uuid.UUID
	gate      chan struct{}
	state     Conversation
	saved     bool
}

type MemoryConversations struct {
	mu      sync.Mutex
	entries map[uuid.UUID]*memoryConversation
}

func NewMemoryConversations() *MemoryConversations {
	return &MemoryConversations{entries: make(map[uuid.UUID]*memoryConversation)}
}

func (m *MemoryConversations) Acquire(
	ctx context.Context,
	patientID, id uuid.UUID,
	access ConversationAccess,
) (LockedConversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	entry, exists := m.entries[id]

	if access.CreateConversation && !exists {
		entry = &memoryConversation{patientID: patientID, gate: make(chan struct{}, 1)}
		// Acquire before publishing so follow-ups cannot precede creation.
		entry.gate <- struct{}{}

		m.entries[id] = entry
	}
	m.mu.Unlock()

	if entry == nil || entry.patientID != patientID || (exists && access.CreateConversation) {
		return nil, domain.ErrNotFound
	}

	if !access.CreateConversation {
		if err := entry.acquire(ctx, access.Wait); err != nil {
			return nil, err
		}
	}

	locked := &lockedMemoryConversation{store: m, id: id, entry: entry}
	if err := ctx.Err(); err != nil {
		locked.Release()
		return nil, err
	}

	// A failed creator can remove the entry while a follow-up waits for access.
	m.mu.Lock()
	present := m.entries[id] == entry
	m.mu.Unlock()

	if !present {
		locked.Release()
		return nil, domain.ErrNotFound
	}

	return locked, nil
}

func (entry *memoryConversation) acquire(ctx context.Context, wait bool) error {
	if !wait {
		select {
		case entry.gate <- struct{}{}:
			return nil
		default:
			return ErrConversationBusy
		}
	}

	select {
	case entry.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type lockedMemoryConversation struct {
	store *MemoryConversations
	id    uuid.UUID
	entry *memoryConversation
}

func (l *lockedMemoryConversation) Snapshot() Conversation {
	return l.entry.state.clone()
}

func (l *lockedMemoryConversation) Save(ctx context.Context, state Conversation) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	l.entry.state = state.clone()
	l.entry.saved = true

	return nil
}

func (l *lockedMemoryConversation) Release() {
	if !l.entry.saved {
		l.store.mu.Lock()
		if l.store.entries[l.id] == l.entry {
			delete(l.store.entries, l.id)
		}
		l.store.mu.Unlock()
	}

	<-l.entry.gate
}
