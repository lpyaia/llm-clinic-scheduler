package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

const appointmentAnswerInstructions = `Após list_appointments, responda somente com o objeto JSON appointment_ids,
contendo todos os IDs da última lista, na mesma ordem.
Se a lista estiver vazia, use appointment_ids: [].`

type appointmentQuery struct {
	core   *application.Service
	schema Schemas
}

func (q *appointmentQuery) Definition() contract.Tool {
	return contract.Tool{
		Name: "list_appointments",
		Description: "Consulta os agendamentos do paciente autenticado. " +
			"Status null usa scheduled; status vazio consulta todos.",
		Parameters: q.schema.ListAppointments,
	}
}

func (q *appointmentQuery) Execute(ctx context.Context, input Input, arguments json.RawMessage) (Outcome, error) {
	filter, err := appointmentFilter(arguments)
	if err != nil {
		return nil, contract.InvalidResponse()
	}

	found, err := q.core.ListAppointments(ctx, input.Principal, filter)
	if err != nil {
		return nil, err
	}

	rows := make([]contract.Appointment, 0, len(found))
	for _, row := range found {
		rows = append(
			rows,
			contract.Appointment{
				ID:         row.ID,
				DoctorName: row.DoctorName,
				Category:   row.Category,
				Status:     row.Status,
				StartsAt:   row.StartsAt.In(q.core.Location),
			},
		)
	}

	result, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}

	return ReturnToModel{
		Content: result,
		ResponseSchema: contract.ResponseSchema{
			Name: "appointment_answer", Schema: q.schema.AppointmentAnswer,
		},
		Instructions: appointmentAnswerInstructions,
		Finalize: func(answer json.RawMessage) (contract.Result, error) {
			return finishAppointments(answer, rows)
		},
	}, nil
}

func finishAppointments(answer json.RawMessage, rows []contract.Appointment) (contract.Result, error) {
	if rows == nil || !matchesAppointments(answer, rows) {
		return contract.Result{}, contract.InvalidResponse()
	}

	return contract.Result{Message: RenderAppointments(rows), Appointments: rows}, nil
}

func appointmentFilter(raw json.RawMessage) (application.AppointmentFilter, error) {
	var args struct {
		Status *string    `json:"status"`
		From   *time.Time `json:"from"`
		To     *time.Time `json:"to"`
	}
	if err := contract.DecodeObject(raw, &args, "status", "from", "to"); err != nil {
		return application.AppointmentFilter{}, err
	}

	filter := application.AppointmentFilter{Status: domain.Scheduled}
	if args.Status != nil {
		filter.Status = *args.Status
	}

	if args.From != nil {
		filter.From = *args.From
	}

	if args.To != nil {
		filter.To = *args.To
	}

	if filter.Status != "" && !domain.ValidStatus(filter.Status) {
		return application.AppointmentFilter{}, contract.InvalidResponse()
	}

	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.To.After(filter.From) {
		return application.AppointmentFilter{}, contract.InvalidResponse()
	}

	return filter, nil
}

func matchesAppointments(raw json.RawMessage, rows []contract.Appointment) bool {
	var answer struct {
		IDs []string `json:"appointment_ids"`
	}
	if contract.DecodeObject(raw, &answer, "appointment_ids") != nil || answer.IDs == nil ||
		len(answer.IDs) != len(rows) {
		return false
	}

	for i, row := range rows {
		if answer.IDs[i] != row.ID.String() {
			return false
		}
	}

	return true
}
