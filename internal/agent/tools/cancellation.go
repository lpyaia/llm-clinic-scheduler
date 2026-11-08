package tools

import (
	"context"
	"encoding/json"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type cancellationPreparation struct {
	core   *application.Service
	schema json.RawMessage
}

func (c *cancellationPreparation) Definition() contract.Tool {
	return contract.Tool{
		Name: "cancel_appointment",
		Description: "Identifica um agendamento próprio futuro e prepara seu cancelamento. " +
			"Pede escolha se ambíguo. Apresenta resumo e exige confirmação em mensagem posterior.",
		Parameters: c.schema,
	}
}

func (c *cancellationPreparation) Execute(ctx context.Context, input Input, raw json.RawMessage) (Outcome, error) {
	candidates, err := findAppointmentCandidates(ctx, c.core, input.Principal, raw)
	if err != nil {
		return nil, err
	}

	if candidates.Selected == nil {
		return ReplyToPatient{
			Result: contract.Result{
				Message:      RenderAppointments(candidates.Rows) + "\nQual agendamento você deseja cancelar?",
				Appointments: candidates.Rows,
			},
			Rescheduling: &ReschedulingUpdate{},
		}, nil
	}

	action, err := c.core.Prepare(ctx, input.Principal, application.PrepareInput{
		Operation: domain.CancelAppointment, AppointmentID: candidates.Selected.ID,
	})
	if err != nil {
		return nil, err
	}

	reply := proposalReply(action, candidates.Selected.Category, contract.BookingSelection{}, c.core.Location)
	reply.Rescheduling = &ReschedulingUpdate{}

	return reply, nil
}
