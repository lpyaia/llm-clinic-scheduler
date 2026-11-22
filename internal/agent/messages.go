package agent

import (
	"context"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// MessageInput identifies a delivery within a patient's conversation. Revision is
// the conversation_revision returned with the proposal the patient actually saw.
// It is required to grant proposal authority, never inferred after waiting.
type MessageInput struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Revision       uint64
	Text           string
}

const (
	MessageProcessing = "processing"
	MessageCompleted  = "completed"
	MessageFailed     = "failed"
)

// MessageRecord is committed with conversation checkpoints. A processing record
// left by interruption must be recovered without interpreting its text again.
type MessageRecord struct {
	ID      uuid.UUID
	Text    string
	Status  string
	Result  *Result
	Failure *domain.Error
}

// ReplyMessage waits for exclusive access before accepting a new message. Lock
// acquisition defines processing order; cancellation while waiting accepts nothing.
// A repeated ID returns the saved outcome and never re-executes tools.
func (a *Agent) ReplyMessage(ctx context.Context, principal domain.Principal, in MessageInput) (Result, error) {
	if in.ID == uuid.Nil {
		return Result{}, domain.Failure("INVALID_INPUT", "Informe um identificador de mensagem válido.")
	}

	return a.replyConversation(ctx, principal, in.ConversationID, in.Text, &in)
}

func (c *Conversation) message(id uuid.UUID) *MessageRecord {
	for i := range c.Messages {
		if c.Messages[i].ID == id {
			return &c.Messages[i]
		}
	}

	return nil
}

func cloneMessageResult(in Result) Result {
	if in.Appointments != nil {
		in.Appointments = append([]Appointment{}, in.Appointments...)
	}

	if in.Availability != nil {
		availability := *in.Availability
		if availability.Slots != nil {
			availability.Slots = append([]Slot{}, availability.Slots...)
		}

		in.Availability = &availability
	}

	if in.PendingAction != nil {
		proposal := *in.PendingAction
		in.PendingAction = &proposal
	}

	return in
}

func (c *Conversation) completeMessage(id uuid.UUID, out Result) {
	if record := c.message(id); record != nil {
		result := cloneMessageResult(out)
		record.Status, record.Result, record.Failure = MessageCompleted, &result, nil
	}
}

func (f *conversationFlow) completeExchange(id, messageID uuid.UUID, text string, out Result) Result {
	out.ConversationID, out.Revision = id, f.state.Revision
	if messageID != uuid.Nil {
		out.MessageID = messageID
		f.state.completeMessage(messageID, out)
	}

	f.rememberExchange(text, out.Message)

	return out
}

// recoverMessages runs only after acquiring exclusive access. Any processing
// record now belongs to an interrupted owner. It never repeats interpretation or
// confirmation: uncertain mutations are queried through their saved action ID.
func (f *conversationFlow) recoverMessages(ctx context.Context, locked LockedConversation, id uuid.UUID) error {
	for i := range f.state.Messages {
		record := &f.state.Messages[i]
		if record.Status != MessageProcessing {
			continue
		}

		err := error(domain.Failure("MESSAGE_INTERRUPTED",
			"O processamento foi interrompido. Envie uma nova mensagem; uma proposta exige novo resumo e confirmação."))

		if f.state.Uncertain != nil {
			var out Result

			out, err = f.recoverConfirmation(ctx)
			if err != nil && f.state.Uncertain != nil {
				return err
			}

			if err == nil {
				f.completeExchange(id, record.ID, record.Text, out)
			}
		}

		if err != nil {
			record.Status, record.Failure = MessageFailed, turnFailure(id, err).failure
		}

		if err := locked.Save(ctx, f.state); err != nil {
			return err
		}
	}

	return nil
}
