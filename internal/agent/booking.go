package agent

import (
	"context"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/tools"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// proposalBinding lives only under the turn's exclusive access. Its identity is
// captured before interpretation; model arguments never choose it. The consumed
// state must be saved before validateBinding or any model/core operation.
type proposalBinding struct {
	ID        uuid.UUID      `json:"-"`
	PatientID uuid.UUID      `json:"-"`
	Revision  uint64         `json:"revision"`
	Proposal  *PendingAction `json:"proposal,omitempty"`
	Completed bool           `json:"completed"`
}

func (f *conversationFlow) bindProposal() bool {
	if f.state.Proposal == nil && f.state.Completed == nil {
		return false
	}

	f.state.Revision++
	f.binding = &proposalBinding{PatientID: f.principal.PatientID, Revision: f.state.Revision}

	if f.state.Proposal != nil {
		f.binding.ID, f.binding.Proposal = f.state.Proposal.ID, f.state.Proposal
	} else {
		f.binding.ID, f.binding.Completed = f.state.Completed.ID, true
	}

	f.state.Proposal, f.state.Completed = nil, nil

	return true
}

func (f *conversationFlow) validateBinding(ctx context.Context) error {
	if f.binding == nil {
		return nil
	}

	action, err := f.core.GetAction(ctx, f.principal, f.binding.ID)
	if err != nil {
		return err
	}

	if action.PatientID != f.binding.PatientID || action.Operation != domain.CreateAppointment {
		return domain.ErrNotFound
	}

	if action.Status == domain.Failed {
		return domain.Failure(action.FailureCode, "Prepare uma nova proposta antes de confirmar.")
	}

	if action.Status != domain.Pending && action.Status != domain.Succeeded {
		return contract.InvalidResponse()
	}

	if f.binding.Completed && action.Status != domain.Succeeded {
		return contract.InvalidResponse()
	}

	if action.Status == domain.Pending && !f.core.Clock.Now().Before(action.ExpiresAt) {
		return domain.ErrActionExpired
	}

	f.binding.Proposal = &PendingAction{
		ID: action.ID, Summary: action.Summary, Category: action.Category,
		ExpiresAt: action.ExpiresAt.In(f.core.Location),
	}

	return nil
}

func (f *conversationFlow) respondToProposal(ctx context.Context, decision string) (Result, error) {
	bound := f.binding
	if bound == nil || bound.PatientID != f.principal.PatientID || bound.Revision != f.state.Revision {
		return Result{}, contract.InvalidResponse()
	}

	f.binding = nil

	switch decision {
	case "confirm":
		return f.confirmBooking(ctx, bound.ID)
	case "reject":
		f.state.Selection = BookingSelection{}
		return tools.Clarification("Proposta descartada. Nenhum novo agendamento foi realizado."), nil
	case "clarify":
		return tools.Clarification(
			"A proposta não foi confirmada. Qual horário e categoria você deseja? " +
				"Leia o novo resumo antes de confirmar.",
		), nil
	default:
		return Result{}, contract.InvalidResponse()
	}
}

func (f *conversationFlow) confirmBooking(ctx context.Context, id uuid.UUID) (Result, error) {
	row, err := f.core.Confirm(ctx, f.principal, id)
	if err != nil {
		return Result{}, err
	}

	f.state.Proposal = nil
	f.state.Completed = &CompletedAction{ID: id}

	rows := []Appointment{{
		ID: row.ID, DoctorName: row.DoctorName, Category: row.Category,
		Status: row.Status, StartsAt: row.StartsAt.In(f.core.Location),
	}}

	return Result{Message: "Agendamento confirmado.\n" + tools.RenderAppointments(rows), Appointments: rows}, nil
}
