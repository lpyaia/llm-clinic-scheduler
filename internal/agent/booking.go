package agent

import (
	"context"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/tools"
)

func (f *conversationFlow) confirmBooking(ctx context.Context) (Result, error) {
	var id uuid.UUID

	if f.state.Proposal != nil {
		id = f.state.Proposal.ID
	} else if f.state.Completed != nil {
		id = f.state.Completed.ID
	}

	if id == uuid.Nil {
		return tools.Clarification(
			"Não há proposta para confirmar nesta conversa. Escolha um horário e uma categoria.",
		), nil
	}

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
