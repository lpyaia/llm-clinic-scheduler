package tools

import (
	"fmt"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// Clarification builds a reply with an empty, non-null appointment list.
func Clarification(message string) contract.Result {
	return contract.Result{Message: message, Appointments: []contract.Appointment{}}
}

func searchClarification(preferences SearchPreferences, message string) ReplyToPatient {
	return ReplyToPatient{Result: Clarification(message), Search: &preferences}
}

func bookingClarification(selection contract.BookingSelection, message string) ReplyToPatient {
	return ReplyToPatient{Result: Clarification(message), Booking: &BookingUpdate{Selection: selection}}
}

func categoryName(category string) string {
	return map[string]string{"consultation": "consulta", "exam": "exame", "procedure": "procedimento"}[category]
}

// RenderAppointments presents only verified application data.
func RenderAppointments(rows []contract.Appointment) string {
	if len(rows) == 0 {
		return "Você não tem atendimentos para os filtros consultados."
	}

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
			categoryName(row.Category),
			row.DoctorName,
			statuses[row.Status],
		)
	}

	return text.String()
}
