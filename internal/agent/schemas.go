package agent

import (
	_ "embed"
	"encoding/json"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/tools"
)

//go:embed schemas/list_appointments.json
var listSchema string

//go:embed schemas/appointment_answer.json
var appointmentAnswerSchema string

//go:embed schemas/find_available_slots.json
var findAvailableSlotsSchema string

//go:embed schemas/create_appointment.json
var createAppointmentSchema string

//go:embed schemas/respond_to_proposal.json
var respondToProposalSchema string

//go:embed schemas/select_appointment.json
var selectAppointmentSchema string

//go:embed schemas/reschedule_appointment.json
var rescheduleAppointmentSchema string

var toolSchemas = tools.Schemas{
	ListAppointments:      json.RawMessage(listSchema),
	AppointmentAnswer:     json.RawMessage(appointmentAnswerSchema),
	FindAvailableSlots:    json.RawMessage(findAvailableSlotsSchema),
	CreateAppointment:     json.RawMessage(createAppointmentSchema),
	RespondToProposal:     json.RawMessage(respondToProposalSchema),
	SelectAppointment:     json.RawMessage(selectAppointmentSchema),
	RescheduleAppointment: json.RawMessage(rescheduleAppointmentSchema),
}
