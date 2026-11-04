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
	Booking BookingState
}

type ConversationTurn struct {
	Create         bool
	ConfirmBooking bool
}

// ConversationStore serializes updates to one conversation and enforces ownership.
// ConfirmBooking must reject a busy conversation: a confirmation cannot wait for
// an unseen proposal. Other turns clear confirmation authority before the callback,
// even on failure or cancellation. Other state commits only on success. Unknown IDs
// cannot be created by a follow-up. Implementations must honor cancellation while waiting.
type ConversationStore interface {
	Update(ctx context.Context, patientID, id uuid.UUID, turn ConversationTurn, update func(*Conversation) error) error
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

func (m *MemoryConversations) Update(
	ctx context.Context,
	patientID, id uuid.UUID,
	turn ConversationTurn,
	update func(*Conversation) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()

	entry, exists := m.entries[id]
	if !exists && turn.Create {
		entry = &memoryConversation{patientID: patientID, gate: make(chan struct{}, 1)}
		m.entries[id] = entry
	}
	m.mu.Unlock()

	if entry == nil || entry.patientID != patientID || (exists && turn.Create) {
		return domain.ErrNotFound
	}

	committed := false
	defer func() {
		if turn.Create && !committed {
			m.mu.Lock()
			delete(m.entries, id)
			m.mu.Unlock()
		}
	}()

	if err := entry.acquire(ctx, turn.ConfirmBooking); err != nil {
		return err
	}
	defer func() { <-entry.gate }()

	if err := ctx.Err(); err != nil {
		return err
	}

	if !turn.ConfirmBooking {
		entry.state.Booking.ActionID = uuid.Nil
	}

	next := entry.state

	next.History = append([]Message(nil), entry.state.History...)
	next.Booking.Offered = append([]Slot(nil), entry.state.Booking.Offered...)
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

func (entry *memoryConversation) acquire(ctx context.Context, confirmation bool) error {
	if confirmation {
		select {
		case entry.gate <- struct{}{}:
			return nil
		default:
			return domain.Failure(
				"INVALID_INPUT",
				"Aguarde a resposta da conversa e leia o resumo antes de confirmar novamente.",
			)
		}
	}

	select {
	case entry.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
