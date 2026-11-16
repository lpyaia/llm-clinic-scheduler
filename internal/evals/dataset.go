// Package evals runs isolated conversations and checks their observable behavior.
package evals

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type Dataset struct {
	Version   int        `json:"version"`
	Scenarios []Scenario `json:"scenarios"`
}

type Scenario struct {
	ID            string    `json:"id"`
	Description   string    `json:"description,omitempty"`
	Now           time.Time `json:"now"`
	Timezone      string    `json:"timezone"`
	PatientID     uuid.UUID `json:"patient_id"`
	ClinicContact string    `json:"clinic_contact,omitempty"`
	Initial       State     `json:"initial"`
	Turns         []Turn    `json:"turns"`
}

// State uses the application's domain values; fixture field names follow those Go types.
type State struct {
	Specialties  []domain.Specialty   `json:"specialties,omitempty"`
	Doctors      []domain.Doctor      `json:"doctors,omitempty"`
	Slots        []domain.Slot        `json:"slots,omitempty"`
	Appointments []domain.Appointment `json:"appointments,omitempty"`
}

type Turn struct {
	Message            string               `json:"message"`
	Advance            string               `json:"advance,omitempty"`
	FailWrite          string               `json:"fail_write,omitempty"`
	UpsertAppointments []domain.Appointment `json:"upsert_appointments,omitempty"`
	Responses          []Completion         `json:"responses,omitempty"`
	Expect             Expectations         `json:"expect"`
}

// Completion is also the portable recording format. Error represents a provider failure.
type Completion struct {
	Content   json.RawMessage  `json:"content,omitempty"`
	ToolCalls []ToolCall       `json:"tool_calls,omitempty"`
	Usage     agent.ModelUsage `json:"usage"`
	Error     string           `json:"error,omitempty"`
	ErrorCode string           `json:"error_code,omitempty"`
	ErrorKind string           `json:"error_kind,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Expectations struct {
	// Tools is the exact ordered call list. Argument objects match a subset of actual fields.
	Tools []ToolCall `json:"tools"`
	// AllowedEffects is a per-turn whitelist: create_appointment, reschedule_appointment, cancel_appointment.
	AllowedEffects []string `json:"allowed_effects"`
	Appointments   *int     `json:"appointments,omitempty"`
	// Stored matches the entire patient appointment set, using a field subset for each distinct row.
	Stored           []json.RawMessage   `json:"stored"`
	ErrorCode        string              `json:"error_code,omitempty"`
	OperationalError bool                `json:"operational_error,omitempty"`
	ErrorContains    []string            `json:"error_contains,omitempty"`
	Response         ResponseExpectation `json:"response"`
}

type ResponseExpectation struct {
	Contains      []string     `json:"contains,omitempty"`
	Absent        []string     `json:"absent,omitempty"`
	PendingAction *bool        `json:"pending_action,omitempty"`
	Availability  *bool        `json:"availability,omitempty"`
	Appointments  *int         `json:"appointments,omitempty"`
	SlotIDs       *[]uuid.UUID `json:"slot_ids,omitempty"`
	From          time.Time    `json:"from,omitempty"`
	Until         time.Time    `json:"until,omitempty"`
}

func Load(reader io.Reader) (Dataset, error) {
	var dataset Dataset

	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&dataset); err != nil {
		return dataset, fmt.Errorf("dataset: %w", err)
	}

	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return dataset, errors.New("dataset must contain exactly one JSON value")
	}

	return dataset, dataset.validate()
}

func (d Dataset) validate() error {
	if d.Version != 1 || len(d.Scenarios) == 0 {
		return errors.New("dataset requires version 1 and at least one scenario")
	}

	seen := make(map[string]bool)
	for _, scenario := range d.Scenarios {
		if scenario.ID == "" || seen[scenario.ID] || scenario.Now.IsZero() ||
			scenario.PatientID == uuid.Nil || len(scenario.Turns) == 0 {
			return fmt.Errorf("invalid or duplicate scenario %q", scenario.ID)
		}

		seen[scenario.ID] = true

		if _, err := time.LoadLocation(scenario.Timezone); err != nil {
			return fmt.Errorf("scenario %s timezone: %w", scenario.ID, err)
		}

		for _, turn := range scenario.Turns {
			if turn.FailWrite != "" && turn.FailWrite != "insert_appointment" &&
				turn.FailWrite != "update_appointment" {
				return fmt.Errorf("scenario %s has invalid fail_write", scenario.ID)
			}

			if turn.Expect.OperationalError && turn.Expect.ErrorCode != "INTERNAL_ERROR" {
				return fmt.Errorf("scenario %s operational_error requires INTERNAL_ERROR", scenario.ID)
			}

			if turn.Message == "" || turn.Expect.Tools == nil || turn.Expect.AllowedEffects == nil {
				return fmt.Errorf("scenario %s requires message, expected tools and allowed_effects", scenario.ID)
			}

			if turn.Advance != "" {
				duration, err := time.ParseDuration(turn.Advance)
				if err != nil || duration < 0 {
					return fmt.Errorf("scenario %s has invalid clock advance", scenario.ID)
				}
			}

			for _, effect := range turn.Expect.AllowedEffects {
				if effect != domain.CreateAppointment && effect != domain.RescheduleAppointment &&
					effect != domain.CancelAppointment {
					return fmt.Errorf("scenario %s has unknown allowed effect %q", scenario.ID, effect)
				}
			}
		}
	}

	return nil
}

// Include patients introduced by later fixture events so snapshots keep a stable scope.
func (s Scenario) otherPatients() []uuid.UUID {
	seen := map[uuid.UUID]bool{s.PatientID: true}
	rows := append([]domain.Appointment(nil), s.Initial.Appointments...)
	for _, turn := range s.Turns {
		rows = append(rows, turn.UpsertAppointments...)
	}

	patients := []uuid.UUID{}

	for _, row := range rows {
		if !seen[row.PatientID] {
			seen[row.PatientID] = true
			patients = append(patients, row.PatientID)
		}
	}

	return patients
}
