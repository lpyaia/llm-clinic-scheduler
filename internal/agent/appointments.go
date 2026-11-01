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

// Appointment contains only the data needed to present a consultation result.
type Appointment struct {
	ID         uuid.UUID `json:"id"`
	DoctorName string    `json:"doctor_name"`
	Category   string    `json:"category"`
	Status     string    `json:"status"`
	StartsAt   time.Time `json:"starts_at"`
}

// appointmentQuery keeps the last successful result within one message execution.
// The loop retains call correlation and model execution policy.
type appointmentQuery struct {
	core      *application.Service
	principal domain.Principal
	rows      []Appointment
}

func (*appointmentQuery) definition() Tool {
	return Tool{
		Name: "list_appointments",
		Description: "Consulta os agendamentos do paciente autenticado. " +
			"Status null usa scheduled; status vazio consulta todos.",
		Parameters: json.RawMessage(listSchema),
	}
}

func (q *appointmentQuery) execute(ctx context.Context, arguments json.RawMessage) (toolOutcome, error) {
	filter, err := appointmentFilter(arguments)
	if err != nil {
		return nil, invalidResponse()
	}

	found, err := q.core.ListAppointments(ctx, q.principal, filter)
	if err != nil {
		return nil, err
	}

	rows := make([]Appointment, 0, len(found))
	for _, row := range found {
		rows = append(
			rows,
			Appointment{
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

	q.rows = rows

	return returnToModel{content: result, finalize: q.finish}, nil
}

func (q *appointmentQuery) finish(answer json.RawMessage) (Result, error) {
	if q.rows == nil || !matchesAppointments(answer, q.rows) {
		return Result{}, invalidResponse()
	}

	return Result{Message: render(q.rows), Appointments: q.rows}, nil
}

func appointmentFilter(raw json.RawMessage) (application.AppointmentFilter, error) {
	var args struct {
		Status *string    `json:"status"`
		From   *time.Time `json:"from"`
		To     *time.Time `json:"to"`
	}
	if err := decodeObject(raw, &args, "status", "from", "to"); err != nil {
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
		return application.AppointmentFilter{}, invalidResponse()
	}

	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.To.After(filter.From) {
		return application.AppointmentFilter{}, invalidResponse()
	}

	return filter, nil
}

func matchesAppointments(raw json.RawMessage, rows []Appointment) bool {
	var answer struct {
		IDs []string `json:"appointment_ids"`
	}
	if decodeObject(raw, &answer, "appointment_ids") != nil || answer.IDs == nil || len(answer.IDs) != len(rows) {
		return false
	}

	for i, row := range rows {
		if answer.IDs[i] != row.ID.String() {
			return false
		}
	}

	return true
}

func render(rows []Appointment) string {
	if len(rows) == 0 {
		return "Você não tem atendimentos para os filtros consultados."
	}

	categories := map[string]string{"consultation": "consulta", "exam": "exame", "procedure": "procedimento"}
	statuses := map[string]string{
		domain.Scheduled: "agendado",
		domain.Cancelled: "cancelado",
		domain.Completed: "concluído",
	}

	var text strings.Builder
	text.WriteString("Seus atendimentos para os filtros consultados:")

	for _, row := range rows {
		fmt.Fprintf(
			&text,
			"\n- %s — %s com %s (%s).",
			row.StartsAt.Format("02/01/2006 às 15:04 -07:00"),
			categories[row.Category],
			row.DoctorName,
			statuses[row.Status],
		)
	}

	return text.String()
}
