package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// BookingState holds the offered choices and the action bound to this conversation.
// ActionID never comes from model arguments and survives successful confirmation for retries.
type BookingState struct {
	Offered  []Slot    `json:"offered_slots"`
	SlotID   uuid.UUID `json:"selected_slot_id"`
	Category string    `json:"category"`
	ActionID uuid.UUID `json:"-"`
}

type PendingAction struct {
	ID        uuid.UUID `json:"id"`
	Summary   string    `json:"summary"`
	Category  string    `json:"category"`
	ExpiresAt time.Time `json:"expires_at"`
}

type bookingPreparation struct {
	core      *application.Service
	principal domain.Principal
	state     *BookingState
}

func (*bookingPreparation) definition() Tool {
	return Tool{
		Name: "create_appointment",
		Description: "Prepara um agendamento para um horário apresentado e categoria escolhida. " +
			"Pede esclarecimento se incompleto. Não reserva nem confirma.",
		Parameters: json.RawMessage(createAppointmentSchema),
	}
}

func (b *bookingPreparation) execute(ctx context.Context, raw json.RawMessage) (toolOutcome, error) {
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
		b.state.SlotID = uuid.Nil
		if *args.SlotID != "" {
			id, err := uuid.Parse(*args.SlotID)
			if err != nil {
				return nil, invalidResponse()
			}

			b.state.SlotID = id
		}
	}

	if args.Category != nil {
		b.state.Category = *args.Category
	}

	found := false

	for _, slot := range b.state.Offered {
		if slot.ID == b.state.SlotID {
			found = true
		}
	}

	if !found {
		b.state.SlotID = uuid.Nil
		return clarification("Qual dos horários apresentados você deseja? Se necessário, faça uma nova busca."), nil
	}

	if !domain.ValidCategory(b.state.Category) {
		return clarification("Qual categoria deseja: consulta, exame ou procedimento?"), nil
	}

	action, err := b.core.Prepare(ctx, b.principal, application.PrepareInput{
		Operation: domain.CreateAppointment,
		SlotID:    b.state.SlotID,
		Category:  b.state.Category,
	})
	if err != nil {
		return nil, err
	}

	b.state.ActionID = action.ID
	proposal := &PendingAction{
		ID: action.ID, Summary: action.Summary, Category: action.Category,
		ExpiresAt: action.ExpiresAt.In(b.core.Location),
	}
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

func explicitConfirmation(message string) bool {
	message = strings.TrimRight(strings.ToLower(strings.TrimSpace(message)), ".!")
	message = strings.Join(strings.Fields(message), " ")

	switch message {
	case "sim", "confirmo", "sim, confirmo", "sim confirmo", "pode agendar", "sim, pode agendar":
		return true
	default:
		return false
	}
}

func explicitRefusal(message string) bool {
	message = strings.TrimRight(strings.ToLower(strings.TrimSpace(message)), ".!")

	switch message {
	case "não", "nao", "não quero", "nao quero", "não confirmo", "nao confirmo", "cancela", "desisto":
		return true
	default:
		return false
	}
}

func (a *Agent) confirmBooking(ctx context.Context, p domain.Principal, id uuid.UUID) (Result, error) {
	if id == uuid.Nil {
		return clarification(
			"Não há proposta para confirmar nesta conversa. Escolha um horário e uma categoria.",
		).result, nil
	}

	row, err := a.core.Confirm(ctx, p, id)
	if err != nil {
		return Result{}, err
	}

	rows := []Appointment{{
		ID: row.ID, DoctorName: row.DoctorName, Category: row.Category,
		Status: row.Status, StartsAt: row.StartsAt.In(a.core.Location),
	}}

	return Result{Message: "Agendamento confirmado.\n" + render(rows), Appointments: rows}, nil
}
