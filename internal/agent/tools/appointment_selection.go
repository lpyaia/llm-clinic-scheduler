package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type appointmentSelection struct {
	core      *application.Service
	schema    json.RawMessage
	operation string
}

func (s *appointmentSelection) Definition() contract.Tool {
	if s.operation == domain.CancelAppointment {
		return contract.Tool{
			Name: "cancel_appointment",
			Description: "Identifica um agendamento próprio futuro e prepara seu cancelamento. " +
				"Pede escolha se ambíguo. Apresenta resumo e exige confirmação em mensagem posterior.",
			Parameters: s.schema,
		}
	}

	return contract.Tool{
		Name: "select_appointment",
		Description: "Identifica um agendamento próprio futuro para remarcar. " +
			"Filtra os dados originais e pede escolha se ambíguo. Não altera reservas.",
		Parameters: s.schema,
	}
}

func (s *appointmentSelection) Execute(ctx context.Context, input Input, raw json.RawMessage) (Outcome, error) {
	var args struct {
		AppointmentID *string    `json:"appointment_id"`
		Doctor        *string    `json:"doctor"`
		Category      *string    `json:"category"`
		From          *time.Time `json:"from"`
		To            *time.Time `json:"to"`
	}
	if err := contract.DecodeObject(raw, &args, "appointment_id", "doctor", "category", "from", "to"); err != nil {
		return nil, contract.InvalidResponse()
	}

	var id uuid.UUID

	if args.AppointmentID != nil && *args.AppointmentID != "" {
		var err error

		id, err = uuid.Parse(*args.AppointmentID)
		if err != nil || id == uuid.Nil {
			return nil, contract.InvalidResponse()
		}
	}

	if args.Category != nil && !domain.ValidCategory(*args.Category) {
		return nil, contract.InvalidResponse()
	}

	filter := application.AppointmentFilter{Status: domain.Scheduled}
	if args.From != nil {
		filter.From = *args.From
	}

	if args.To != nil {
		filter.To = *args.To
	}

	found, err := s.core.ListAppointments(ctx, input.Principal, filter)
	if err != nil {
		return nil, err
	}

	rows := []contract.Appointment{}

	var selected *contract.Rescheduling

	for _, row := range found {
		if domain.ValidateChange(row, s.core.Clock.Now()) != nil ||
			(id != uuid.Nil && row.ID != id) ||
			(args.Category != nil && row.Category != *args.Category) ||
			(args.Doctor != nil && !matchesDoctorName(*args.Doctor, row.DoctorName)) {
			continue
		}

		appointment := contract.Appointment{
			ID: row.ID, DoctorName: row.DoctorName, Category: row.Category,
			Status: row.Status, StartsAt: row.StartsAt.In(s.core.Location),
		}
		rows = append(rows, appointment)
		selected = &contract.Rescheduling{Appointment: appointment, DoctorID: row.DoctorID, SlotID: row.SlotID}
	}

	message := RenderAppointments(rows) + "\nQual agendamento você deseja remarcar?"
	if len(rows) != 1 || (args.AppointmentID != nil && *args.AppointmentID == "") {
		selected = nil
	} else {
		message = RenderAppointments(rows) + "\nPara qual novo horário deseja remarcar com o mesmo médico?"
	}

	if s.operation == domain.CancelAppointment {
		if selected == nil {
			return ReplyToPatient{
				Result: contract.Result{
					Message: RenderAppointments(rows) + "\nQual agendamento você deseja cancelar?", Appointments: rows,
				},
				Rescheduling: &ReschedulingUpdate{},
			}, nil
		}

		action, err := s.core.Prepare(ctx, input.Principal, application.PrepareInput{
			Operation: domain.CancelAppointment, AppointmentID: selected.ID,
		})
		if err != nil {
			return nil, err
		}

		reply := proposalReply(action, selected.Category, contract.BookingSelection{}, s.core.Location)
		reply.Rescheduling = &ReschedulingUpdate{}

		return reply, nil
	}

	preferences := SearchPreferences{}
	if selected != nil {
		preferences.criteria.Doctor = selected.DoctorName
	}

	return ReplyToPatient{
		Result: contract.Result{Message: message, Appointments: rows}, Search: &preferences,
		Rescheduling: &ReschedulingUpdate{Selected: selected},
	}, nil
}
