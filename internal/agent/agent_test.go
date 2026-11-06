package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type modelFunc func(context.Context, agent.Request) (agent.Response, error)

func (f modelFunc) Complete(ctx context.Context, in agent.Request) (agent.Response, error) {
	return f(ctx, in)
}

func TestModelFailureAndCancellation(t *testing.T) {
	want := errors.New("provider failed")
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{}, want
	})
	a := agent.New(&application.Service{}, model)

	_, err := a.Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
	if !errors.Is(err, want) {
		t.Fatalf("model error not preserved: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = a.Reply(ctx, domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
}

func TestAgentStopsAnEndlessToolLoop(t *testing.T) {
	step := 0
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		step++
		if step > agent.MaxSteps {
			t.Fatal("agent exceeded its model-call budget")
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{
				{ID: fmt.Sprintf("call-%d", step), Name: "list_appointments", Arguments: json.RawMessage(`{}`)},
			},
		}, nil
	})
	core := &application.Service{Repo: repository{}}

	_, err := agent.New(core, model).
		Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
	if domain.ErrorCode(err) != "AGENT_STEP_LIMIT" {
		t.Fatalf("expected step limit, got %v", err)
	}
}

func TestInvalidModelResponsesDoNotExecuteTools(t *testing.T) {
	valid := agent.ToolCall{ID: "call", Name: "list_appointments", Arguments: json.RawMessage(`{}`)}
	for _, response := range []agent.Response{
		{},
		{Content: json.RawMessage(`{"appointment_ids":[]}`)}, // No successful query yet.
		{ToolCalls: []agent.ToolCall{{ID: "call", Name: "cancel_appointment", Arguments: json.RawMessage(`{}`)}}},
		{ToolCalls: []agent.ToolCall{{Name: "list_appointments", Arguments: json.RawMessage(`{}`)}}},
		{ToolCalls: []agent.ToolCall{valid, valid}},
		{ToolCalls: []agent.ToolCall{valid}, Content: json.RawMessage(`{"appointment_ids":[]}`)},
	} {
		model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) { return response, nil })

		_, err := agent.New(&application.Service{}, model).
			Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
		if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
			t.Fatalf("response=%+v error=%v", response, err)
		}
	}
}

func TestCancellationAfterModelReplyPreventsToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		cancel()

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "query", Name: "list_appointments", Arguments: json.RawMessage(`{}`)}},
		}, nil
	})

	_, err := agent.New(&application.Service{}, model).
		Reply(ctx, domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation before executing tool: %v", err)
	}
}

type repository struct {
	application.Repository
	rows []domain.Appointment
}

func (r repository) Appointments(
	_ context.Context,
	patientID uuid.UUID,
	filter application.AppointmentFilter,
) ([]domain.Appointment, error) {
	var out []domain.Appointment

	for _, row := range r.rows {
		if row.PatientID == patientID && (filter.Status == "" || row.Status == filter.Status) {
			out = append(out, row)
		}
	}

	return out, nil
}
