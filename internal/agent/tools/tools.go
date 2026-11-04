// Package tools implements scheduling tools without access to conversation state or the model loop.
package tools

import (
	"context"
	"encoding/json"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// Executor separates tool definitions and execution from model-loop policy.
// Execute receives a snapshot, never conversation storage. It must not mutate
// supplied slices. On error the caller discards the outcome; successful updates
// are applied by the flow and persisted only after a valid reply.
type Executor interface {
	Definition() contract.Tool
	Execute(context.Context, Input, json.RawMessage) (Outcome, error)
}

// Input carries application-authenticated identity and explicit conversation values.
// Model arguments cannot override Principal. Offered is a read-only snapshot.
type Input struct {
	Principal domain.Principal
	Search    SearchPreferences
	Selection contract.BookingSelection
	Offered   []contract.Slot
}

// Schemas are embedded by the agent composition root, independent of execution.
type Schemas struct {
	ListAppointments   json.RawMessage
	AppointmentAnswer  json.RawMessage
	FindAvailableSlots json.RawMessage
	CreateAppointment  json.RawMessage
}

// New supplies the three supported tools; it is not a dynamic plugin registry.
func New(core *application.Service, schemas Schemas) []Executor {
	return []Executor{
		&appointmentQuery{core: core, schema: schemas},
		&slotSearch{core: core, schema: schemas},
		&bookingPreparation{core: core, schema: schemas},
	}
}

// Outcome is either a model continuation or a complete patient reply.
type Outcome interface {
	toolOutcome()
}

// AnswerFinalizer verifies a later model answer against this execution's data.
// It captures only the returned query rows, performs no I/O and never re-enters
// the flow or executor. The loop replaces it on each query and drops it at turn end.
type AnswerFinalizer func(json.RawMessage) (contract.Result, error)

type ReturnToModel struct {
	Content        json.RawMessage
	ResponseSchema contract.ResponseSchema
	Instructions   string
	Finalize       AnswerFinalizer
}

func (ReturnToModel) toolOutcome() {}

// ReplyToPatient returns owned updates for the flow to apply. Nil means no
// update to that part of the state. Search replies include preferences even
// when clarification is needed; booking replies include the incomplete selection.
type ReplyToPatient struct {
	Result  contract.Result
	Search  *SearchPreferences
	Booking *BookingUpdate
}

func (ReplyToPatient) toolOutcome() {}

type BookingUpdate struct {
	Selection contract.BookingSelection
	Proposal  *contract.PendingAction
}
