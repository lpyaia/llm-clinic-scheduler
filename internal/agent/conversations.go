package agent

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type Conversation struct {
	Intent  string
	Search  SearchPreferences
	History []Message
}

// ConversationStore serializes updates to one conversation, enforces ownership,
// and commits the callback's state only on success. Unknown IDs cannot be created
// by a follow-up. Implementations must honor cancellation while waiting.
type ConversationStore interface {
	Update(ctx context.Context, patientID, id uuid.UUID, create bool, update func(*Conversation) error) error
}

type memoryConversation struct {
	patientID uuid.UUID
	gate      chan struct{}
	state     Conversation
}

type MemoryConversations struct {
	mu      sync.Mutex
	entries map[uuid.UUID]*memoryConversation
}

func NewMemoryConversations() *MemoryConversations {
	return &MemoryConversations{entries: make(map[uuid.UUID]*memoryConversation)}
}

func (m *MemoryConversations) Update(ctx context.Context, patientID, id uuid.UUID, create bool, update func(*Conversation) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()

	entry, exists := m.entries[id]
	if !exists && create {
		entry = &memoryConversation{patientID: patientID, gate: make(chan struct{}, 1)}
		m.entries[id] = entry
	}
	m.mu.Unlock()

	if entry == nil || entry.patientID != patientID || (exists && create) {
		return domain.ErrNotFound
	}

	committed := false
	defer func() {
		if create && !committed {
			m.mu.Lock()
			delete(m.entries, id)
			m.mu.Unlock()
		}
	}()

	select {
	case entry.gate <- struct{}{}:
		defer func() { <-entry.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	next := entry.state
	next.History = append([]Message(nil), entry.state.History...)
	if err := update(&next); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	entry.state = next
	committed = true

	return nil
}
