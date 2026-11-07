// Package contract defines provider-independent messages and conversation data.
package contract

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Request struct {
	Messages []Message
	Tools    []Tool
	// RequireTool requests a tool call instead of a final answer.
	RequireTool    bool
	ResponseSchema *ResponseSchema
}

// ResponseSchema describes the structured answer expected by the active tool.
// A nil schema leaves the model request without a structured final-answer format.
type ResponseSchema struct {
	Name   string
	Schema json.RawMessage
}

type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type Tool struct {
	Name, Description string
	Parameters        json.RawMessage
}

type ToolCall struct {
	ID, Name  string
	Arguments json.RawMessage
}

// Response contains either tool calls or a structured final answer, never both.
type Response struct {
	Content   json.RawMessage
	ToolCalls []ToolCall
}

type Result struct {
	ConversationID uuid.UUID      `json:"conversation_id"`
	Message        string         `json:"message"`
	Appointments   []Appointment  `json:"appointments"`
	Availability   *Availability  `json:"availability,omitempty"`
	PendingAction  *PendingAction `json:"pending_action,omitempty"`
}

// Appointment contains only the data needed to present a consultation result.
type Appointment struct {
	ID         uuid.UUID `json:"id"`
	DoctorName string    `json:"doctor_name"`
	Category   string    `json:"category"`
	Status     string    `json:"status"`
	StartsAt   time.Time `json:"starts_at"`
}

type Slot struct {
	ID         uuid.UUID `json:"id"`
	DoctorName string    `json:"doctor_name"`
	StartsAt   time.Time `json:"starts_at"`
}

type Availability struct {
	Slots    []Slot    `json:"slots"`
	From     time.Time `json:"searched_from"`
	Until    time.Time `json:"searched_until"`
	Expanded bool      `json:"expanded"`
}

type PendingAction struct {
	ID        uuid.UUID `json:"id"`
	Summary   string    `json:"summary"`
	Category  string    `json:"category"`
	ExpiresAt time.Time `json:"expires_at"`
}

type BookingSelection struct {
	SlotID   uuid.UUID `json:"selected_slot_id"`
	Category string    `json:"category"`
}

// Rescheduling retains a verified original; model arguments cannot change its doctor or category.
type Rescheduling struct {
	Appointment
	DoctorID uuid.UUID `json:"-"`
	SlotID   uuid.UUID `json:"-"`
}
