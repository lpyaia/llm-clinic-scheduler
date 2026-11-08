package agent

import (
	"context"

	"github.com/lpyaia/llm-clinic-scheduler/internal/trace"
)

// tracedConversation records every durable save, including confirmation checkpoints
// and failure cleanup, without exporting state. The original handle still owns access;
// Snapshot and Release retain their behavior and the turn owner controls save ordering.
type tracedConversation struct {
	LockedConversation
	run *trace.Run
}

func (l *tracedConversation) Save(ctx context.Context, state Conversation) error {
	step := l.run.StartStep(trace.Step{Kind: "conversation.save"}, nil)
	err := l.LockedConversation.Save(ctx, state)
	step.End(nil, err)

	return err
}
