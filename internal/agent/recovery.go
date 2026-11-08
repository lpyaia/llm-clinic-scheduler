package agent

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// TurnError exposes safe recovery guidance while preserving the original cause
// for errors.Is/errors.As. ConversationID identifies the saved recovery context.
type TurnError struct {
	ConversationID uuid.UUID
	failure        *domain.Error
	cause          error
}

func (e *TurnError) Error() string   { return e.failure.Error() }
func (e *TurnError) Unwrap() []error { return []error{e.failure, e.cause} }

func turnFailure(id uuid.UUID, err error) error {
	code := domain.ErrorCode(err)
	message := "Não foi possível concluir a operação. Consulte seus agendamentos antes de tentar novamente. " +
		"Uma nova operação exige outro resumo e confirmação."

	switch code {
	case "SLOT_UNAVAILABLE":
		message = "Este horário não está mais disponível. Deseja fazer uma nova busca de horários? " +
			"Escolha uma nova proposta e leia o resumo antes de confirmar."
	case "WEEKLY_LIMIT_REACHED":
		message = "Você já possui três agendamentos na semana do horário escolhido. " +
			"Podemos buscar em outra semana ou consultar seus agendamentos para remarcar um deles."
	case "ACTION_EXPIRED":
		message = "A proposta expirou e foi descartada. Prepare uma nova proposta, " +
			"leia o novo resumo e confirme novamente."
	case "APPOINTMENT_CHANGED":
		message = "O agendamento mudou e a proposta foi descartada. Consulte seus agendamentos, " +
			"prepare uma nova proposta e confirme somente após ler o novo resumo."
	case "MODEL_INVALID_RESPONSE", "AGENT_STEP_LIMIT":
		message = "Não consegui interpretar seu pedido com segurança. Envie o pedido novamente. " +
			"Uma proposta anterior exige nova preparação, resumo e confirmação."
	case "NOT_FOUND", "UNAUTHORIZED", "INVALID_INPUT", "APPOINTMENT_NOT_MUTABLE", "ACTION_NOT_COMPLETED":
		var failure *domain.Error
		if errors.As(err, &failure) {
			message = failure.Message
		}
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		message = "O atendimento foi interrompido. Consulte o resultado nesta conversa antes de tentar novamente. " +
			"Uma nova operação exige outro resumo e confirmação."
	}

	return &TurnError{ConversationID: id, failure: domain.Failure(code, message), cause: err}
}

// failTurn preserves rollback of preferences/history, but terminal business failures
// discard obsolete choices. Unknown confirmation outcomes keep their lookup reference.
func failTurn(ctx context.Context, locked LockedConversation, id uuid.UUID, err error) error {
	state := locked.Snapshot()
	if state.discardFailedProposal(domain.ErrorCode(err)) {
		if saveErr := locked.Save(ctx, state); saveErr != nil {
			err = errors.Join(err, saveErr)
		}
	}

	return turnFailure(id, err)
}

func (c *Conversation) discardFailedProposal(code string) bool {
	switch code {
	case "SLOT_UNAVAILABLE":
		c.Offered = nil
	case "APPOINTMENT_CHANGED", "APPOINTMENT_NOT_MUTABLE", "NOT_FOUND":
		c.Rescheduling, c.Candidates, c.Offered = nil, nil, nil
	case "WEEKLY_LIMIT_REACHED", "ACTION_EXPIRED":
		// The offered slots may be reused only to prepare a fresh proposal.
	default:
		return false
	}

	c.Proposal, c.Completed, c.Uncertain = nil, nil, nil
	c.Selection = BookingSelection{}

	return true
}

// recoverConfirmation runs before interpretation of a subsequent message. It only
// queries the core: even a still-pending action needs a fresh proposal and consent.
func (f *conversationFlow) recoverConfirmation(ctx context.Context) (Result, error) {
	action, err := f.core.GetAction(ctx, f.principal, f.state.Uncertain.ID)
	if err != nil {
		return Result{}, err
	}

	if action.PatientID != f.principal.PatientID ||
		(action.Operation != domain.CreateAppointment && action.Operation != domain.RescheduleAppointment &&
			action.Operation != domain.CancelAppointment) {
		return Result{}, domain.ErrNotFound
	}

	switch action.Status {
	case domain.Succeeded:
		if action.Result == nil {
			return Result{}, domain.Failure("INTERNAL_ERROR", "Resultado da confirmação indisponível.")
		}

		out := f.confirmedResult(*action.Result, action.ID, action.Operation)
		out.Message = "Consultei o resultado da confirmação anterior.\n" + out.Message +
			"\nSe deseja outra operação, envie o pedido novamente."

		return out, nil
	case domain.Pending, domain.Failed:
		f.state.Uncertain = nil
		f.state.Selection = BookingSelection{}
		if action.Status == domain.Failed {
			f.state.discardFailedProposal(action.FailureCode)

			return Result{}, domain.Failure(action.FailureCode, "Prepare uma nova proposta antes de confirmar.")
		}

		return Result{}, domain.Failure(
			"ACTION_NOT_COMPLETED",
			"A confirmação não foi concluída. Prepare uma nova proposta e confirme após ler o novo resumo.",
		)
	default:
		return Result{}, domain.Failure("INTERNAL_ERROR", "Estado da confirmação indisponível.")
	}
}
