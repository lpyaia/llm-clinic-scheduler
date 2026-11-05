// Package agent coordinates the conversational interface over the scheduling application.
package agent

import (
	"context"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/tools"
)

// Model is the provider boundary. Implementations must honor context cancellation.
type Model interface {
	Complete(context.Context, Request) (Response, error)
}

// Aliases preserve the public agent API while contracts are owned below the loop.
type Request = contract.Request
type ResponseSchema = contract.ResponseSchema
type Message = contract.Message
type Tool = contract.Tool
type ToolCall = contract.ToolCall
type Response = contract.Response
type Result = contract.Result
type Appointment = contract.Appointment
type Slot = contract.Slot
type Availability = contract.Availability
type PendingAction = contract.PendingAction
type BookingSelection = contract.BookingSelection
type SearchPreferences = tools.SearchPreferences
