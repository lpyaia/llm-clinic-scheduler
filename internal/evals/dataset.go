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
	Message   string       `json:"message"`
	Advance   string       `json:"advance,omitempty"`
	Responses []Completion `json:"responses,omitempty"`
	Expect    Expectations `json:"expect"`
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
	AllowedEffects []string            `json:"allowed_effects"`
	Appointments   *int                `json:"appointments,omitempty"`
	ErrorCode      string              `json:"error_code,omitempty"`
	Response       ResponseExpectation `json:"response"`
}

type ResponseExpectation struct {
	Contains      []string `json:"contains,omitempty"`
	Absent        []string `json:"absent,omitempty"`
	PendingAction *bool    `json:"pending_action,omitempty"`
	Availability  *bool    `json:"availability,omitempty"`
	Appointments  *int     `json:"appointments,omitempty"`
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
