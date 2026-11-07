package tools

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type reschedulingPreparation struct {
	core   *application.Service
	schema json.RawMessage
}

func (r *reschedulingPreparation) Definition() contract.Tool {
	return contract.Tool{
		Name: "reschedule_appointment",
		Description: "Prepara a remarcação do agendamento selecionado para um horário apresentado do mesmo médico. " +
			"Preserva categoria e ID. Não executa sem confirmação posterior.",
		Parameters: r.schema,
	}
}

func (r *reschedulingPreparation) Execute(ctx context.Context, input Input, raw json.RawMessage) (Outcome, error) {
	var args struct {
		SlotID *string `json:"slot_id"`
	}
	if err := contract.DecodeObject(raw, &args, "slot_id"); err != nil {
		return nil, contract.InvalidResponse()
	}

	if input.Rescheduling == nil {
		return ReplyToPatient{Result: Clarification("Qual dos seus agendamentos deseja remarcar?")}, nil
	}

	if args.SlotID == nil || *args.SlotID == "" {
		return bookingClarification(contract.BookingSelection{}, "Qual dos novos horários apresentados deseja?"), nil
	}

	id, err := uuid.Parse(*args.SlotID)
	if err != nil {
		return nil, contract.InvalidResponse()
	}

	found := false

	for _, slot := range input.Offered {
		if slot.ID == id && id != input.Rescheduling.SlotID {
			found = true
		}
	}

	if !found {
		return bookingClarification(contract.BookingSelection{}, "Escolha um novo horário da busca apresentada."), nil
	}

	action, err := r.core.Prepare(ctx, input.Principal, application.PrepareInput{
		Operation: domain.RescheduleAppointment, AppointmentID: input.Rescheduling.ID, SlotID: id,
	})
	if err != nil {
		return nil, err
	}

	return proposalReply(action, input.Rescheduling.Category, contract.BookingSelection{}, r.core.Location), nil
}
