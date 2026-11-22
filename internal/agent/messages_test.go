package agent_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func TestIdentifiedMessageReplaySurvivesAgentRestart(t *testing.T) {
	core, _ := bookingFixture()
	p := domain.Principal{PatientID: uuid.New()}
	store := agent.NewMemoryConversations()
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return toolReply("search", "find_available_slots", `{"doctor":"Ana"}`), nil
	})

	input := agent.MessageInput{ID: uuid.New(), Text: "Horários com Ana"}
	out, err := agent.NewWithConversations(core, model, store).ReplyMessage(context.Background(), p, input)
	if err != nil || out.MessageID != input.ID || out.ConversationID == uuid.Nil {
		t.Fatalf("identified message failed: %+v err=%v", out, err)
	}

	restarted := agent.NewWithConversations(
		core,
		modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
			t.Fatal("replay must not call the model or execute tools")
			return agent.Response{}, nil
		}),
		store,
	)

	replayed, err := restarted.ReplyMessage(context.Background(), p, input)
	if err != nil || !reflect.DeepEqual(replayed, out) {
		t.Fatalf("initial-message retry changed its result: %+v err=%v", replayed, err)
	}

	input.ConversationID = out.ConversationID
	replayed, err = restarted.ReplyMessage(context.Background(), p, input)
	if err != nil || !reflect.DeepEqual(replayed, out) {
		t.Fatalf("conversation retry changed its result: %+v err=%v", replayed, err)
	}

	replayed, err = agent.NewWithConversations(core, nil, store).ReplyMessage(context.Background(), p, input)
	if err != nil || !reflect.DeepEqual(replayed, out) {
		t.Fatalf("saved outcome depended on model availability: %+v err=%v", replayed, err)
	}
}

func TestFailedIdentifiedMessageIsNotExecutedAgain(t *testing.T) {
	core, _ := bookingFixture()
	p := domain.Principal{PatientID: uuid.New()}
	store := agent.NewMemoryConversations()
	input := agent.MessageInput{ID: uuid.New(), Text: "Horários com Ana"}
	providerErr := errors.New("provider unavailable")
	a := agent.NewWithConversations(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{}, providerErr
	}), store)

	_, err := a.ReplyMessage(context.Background(), p, input)
	if !errors.Is(err, providerErr) {
		t.Fatalf("missing provider error: %v", err)
	}

	restarted := agent.NewWithConversations(
		core,
		modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
			t.Fatal("a failed message cannot execute again with the same ID")
			return agent.Response{}, nil
		}),
		store,
	)

	_, err = restarted.ReplyMessage(context.Background(), p, input)
	if domain.ErrorCode(err) != "INTERNAL_ERROR" {
		t.Fatalf("failed message outcome changed: %v", err)
	}
}

func TestInterruptedIdentifiedMessageDoesNotRestartInterpretation(t *testing.T) {
	core, _ := bookingFixture()
	p := domain.Principal{PatientID: uuid.New()}
	store := agent.NewMemoryConversations()
	input := agent.MessageInput{ID: uuid.New(), Text: "Horários com Ana"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := agent.NewWithConversations(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		cancel()
		return agent.Response{}, context.Canceled
	}), store)

	_, err := a.ReplyMessage(ctx, p, input)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("missing interruption: %v", err)
	}

	restarted := agent.NewWithConversations(
		core,
		modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
			t.Fatal("an interrupted message must not restart interpretation")
			return agent.Response{}, nil
		}),
		store,
	)

	_, err = restarted.ReplyMessage(context.Background(), p, input)
	if domain.ErrorCode(err) != "MESSAGE_INTERRUPTED" {
		t.Fatalf("interrupted message was not recovered safely: %v", err)
	}
}
