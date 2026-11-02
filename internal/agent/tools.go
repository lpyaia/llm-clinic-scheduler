package agent

import (
	"context"
	"encoding/json"
)

// toolExecutor is the execution seam shared by the conversation's tools.
// Implementations are created for each message and receive only model arguments;
// authenticated identity and conversation preferences are bound by the caller.
type toolExecutor interface {
	definition() Tool
	execute(context.Context, json.RawMessage) (toolOutcome, error)
}

// toolOutcome has two variants: returnToModel and replyToPatient.
type toolOutcome interface {
	toolOutcome()
}

type answerFinalizer func(json.RawMessage) (Result, error)

// returnToModel supplies tool data, the named answer schema and instructions,
// and verification of a later model answer.
type returnToModel struct {
	content        json.RawMessage
	responseSchema ResponseSchema
	instructions   string
	finalize       answerFinalizer
}

func (returnToModel) toolOutcome() {}

// replyToPatient completes the current message, including clarification replies.
type replyToPatient struct {
	result Result
}

func (replyToPatient) toolOutcome() {}
