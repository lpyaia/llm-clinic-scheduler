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

type appointmentCandidates struct {
	Rows     []contract.Appointment
	Selected *domain.Appointment
}

// findAppointmentCandidates shares only argument validation and eligible-appointment lookup.
// Each tool owns its reply, preparation and conversation updates.
func findAppointmentCandidates(
	ctx context.Context, core *application.Service, principal domain.Principal, raw json.RawMessage,
) (appointmentCandidates, error) {
	var args struct {
		AppointmentID *string    `json:"appointment_id"`
		Doctor        *string    `json:"doctor"`
		Category      *string    `json:"category"`
		From          *time.Time `json:"from"`
		To            *time.Time `json:"to"`
	}
	if err := contract.DecodeObject(raw, &args, "appointment_id", "doctor", "category", "from", "to"); err != nil {
		return appointmentCandidates{}, contract.InvalidResponse()
	}

	var id uuid.UUID

	if args.AppointmentID != nil && *args.AppointmentID != "" {
		var err error

		id, err = uuid.Parse(*args.AppointmentID)
		if err != nil || id == uuid.Nil {
			return appointmentCandidates{}, contract.InvalidResponse()
		}
	}

	if args.Category != nil && !domain.ValidCategory(*args.Category) {
		return appointmentCandidates{}, contract.InvalidResponse()
	}

	filter := application.AppointmentFilter{Status: domain.Scheduled}
	if args.From != nil {
		filter.From = *args.From
	}

	if args.To != nil {
		filter.To = *args.To
	}

	found, err := core.ListAppointments(ctx, principal, filter)
	if err != nil {
		return appointmentCandidates{}, err
	}

	candidates := appointmentCandidates{Rows: []contract.Appointment{}}

	for _, row := range found {
		if domain.ValidateChange(row, core.Clock.Now()) != nil ||
			(id != uuid.Nil && row.ID != id) ||
			(args.Category != nil && row.Category != *args.Category) ||
			(args.Doctor != nil && !matchesDoctorName(*args.Doctor, row.DoctorName)) {
			continue
		}

		candidates.Rows = append(candidates.Rows, contract.Appointment{
			ID: row.ID, DoctorName: row.DoctorName, Category: row.Category,
			Status: row.Status, StartsAt: row.StartsAt.In(core.Location),
		})
		candidates.Selected = &row
	}

	if len(candidates.Rows) != 1 || (args.AppointmentID != nil && *args.AppointmentID == "") {
		candidates.Selected = nil
	}

	return candidates, nil
}
