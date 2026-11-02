// Package agent coordinates the conversational interface over the scheduling application.
package agent

import (
	"context"
	"encoding/json"
)

// Model is the provider boundary. Implementations must honor context cancellation.
type Model interface {
	Complete(context.Context, Request) (Response, error)
}

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
