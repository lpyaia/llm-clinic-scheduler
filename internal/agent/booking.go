package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type PendingAction struct {
	ID        uuid.UUID `json:"id"`
	Summary   string    `json:"summary"`
	Category  string    `json:"category"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (*conversationFlow) definition() Tool {
	return Tool{
		Name: "create_appointment",
		Description: "Prepara um agendamento para um horário apresentado e categoria escolhida. " +
			"Pede esclarecimento se incompleto. Não reserva nem confirma.",
		Parameters: json.RawMessage(createAppointmentSchema),
	}
}

func (f *conversationFlow) execute(ctx context.Context, raw json.RawMessage) (toolOutcome, error) {
	var args struct {
		SlotID   *string `json:"slot_id"`
		Category *string `json:"category"`
	}
	if err := decodeObject(raw, &args, "slot_id", "category"); err != nil {
		return nil, invalidResponse()
	}

	if args.SlotID == nil && args.Category == nil {
		return clarification("Qual horário e categoria você deseja? A proposta anterior não foi confirmada."), nil
	}

	if args.SlotID != nil {
		f.state.Selection.SlotID = uuid.Nil
		if *args.SlotID != "" {
			id, err := uuid.Parse(*args.SlotID)
			if err != nil {
				return nil, invalidResponse()
			}

			f.state.Selection.SlotID = id
		}
	}

	if args.Category != nil {
		f.state.Selection.Category = *args.Category
	}

	found := false

	for _, slot := range f.state.Offered {
		if slot.ID == f.state.Selection.SlotID {
			found = true
		}
	}

	if !found {
		f.state.Selection.SlotID = uuid.Nil
		return clarification("Qual dos horários apresentados você deseja? Se necessário, faça uma nova busca."), nil
	}

	if !domain.ValidCategory(f.state.Selection.Category) {
		return clarification("Qual categoria deseja: consulta, exame ou procedimento?"), nil
	}

	action, err := f.core.Prepare(ctx, f.principal, application.PrepareInput{
		Operation: domain.CreateAppointment,
		SlotID:    f.state.Selection.SlotID,
		Category:  f.state.Selection.Category,
	})
	if err != nil {
		return nil, err
	}

	proposal := &PendingAction{
		ID: action.ID, Summary: action.Summary, Category: action.Category,
		ExpiresAt: action.ExpiresAt.In(f.core.Location),
	}
	f.state.Proposal = proposal

	result := Result{
		Message: fmt.Sprintf(
			"%s Categoria: %s. Válida até %s. Você confirma? Responda sim, confirmo para agendar.",
			proposal.Summary, categoryName(proposal.Category), proposal.ExpiresAt.Format("02/01/2006 às 15:04 -07:00"),
		),
		Appointments: []Appointment{}, PendingAction: proposal,
	}

	return replyToPatient{result: result}, nil
}

func categoryName(category string) string {
	return map[string]string{"consultation": "consulta", "exam": "exame", "procedure": "procedimento"}[category]
}

func (f *conversationFlow) confirmBooking(ctx context.Context) (Result, error) {
	var id uuid.UUID

	if f.state.Proposal != nil {
		id = f.state.Proposal.ID
	} else if f.state.Completed != nil {
		id = f.state.Completed.ID
	}

	if id == uuid.Nil {
		return clarification(
			"Não há proposta para confirmar nesta conversa. Escolha um horário e uma categoria.",
		).result, nil
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

	return Result{Message: "Agendamento confirmado.\n" + render(rows), Appointments: rows}, nil
}
