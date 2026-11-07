package tools

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type bookingPreparation struct {
	core   *application.Service
	schema Schemas
}

func (b *bookingPreparation) Definition() contract.Tool {
	return contract.Tool{
		Name: "create_appointment",
		Description: "Prepara um agendamento para um horário apresentado e categoria escolhida. " +
			"Pede esclarecimento se incompleto. Não reserva nem confirma.",
		Parameters: b.schema.CreateAppointment,
	}
}

func (b *bookingPreparation) Execute(ctx context.Context, input Input, raw json.RawMessage) (Outcome, error) {
	selection := input.Selection

	var args struct {
		SlotID   *string `json:"slot_id"`
		Category *string `json:"category"`
	}
	if err := contract.DecodeObject(raw, &args, "slot_id", "category"); err != nil {
		return nil, contract.InvalidResponse()
	}

	if args.SlotID == nil && args.Category == nil {
		return bookingClarification(
			selection,
			"Qual horário e categoria você deseja? A proposta anterior não foi confirmada.",
		), nil
	}

	if args.SlotID != nil {
		selection.SlotID = uuid.Nil
		if *args.SlotID != "" {
			id, err := uuid.Parse(*args.SlotID)
			if err != nil {
				return nil, contract.InvalidResponse()
			}

			selection.SlotID = id
		}
	}

	if args.Category != nil {
		selection.Category = *args.Category
	}

	found := false

	for _, slot := range input.Offered {
		if slot.ID == selection.SlotID {
			found = true
		}
	}

	if !found {
		selection.SlotID = uuid.Nil

		return bookingClarification(
			selection,
			"Qual dos horários apresentados você deseja? Se necessário, faça uma nova busca.",
		), nil
	}

	if !domain.ValidCategory(selection.Category) {
		return bookingClarification(selection, "Qual categoria deseja: consulta, exame ou procedimento?"), nil
	}

	action, err := b.core.Prepare(ctx, input.Principal, application.PrepareInput{
		Operation: domain.CreateAppointment,
		SlotID:    selection.SlotID,
		Category:  selection.Category,
	})
	if err != nil {
		return nil, err
	}

	return proposalReply(action, action.Category, selection, b.core.Location), nil
}
