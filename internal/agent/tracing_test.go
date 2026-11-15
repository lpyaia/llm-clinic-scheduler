package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"github.com/lpyaia/llm-clinic-scheduler/internal/trace"
)

func TestTraceCorrelatesInputModelToolAndFinalReply(t *testing.T) {
	var output bytes.Buffer

	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		if in.Messages[len(in.Messages)-1].Role == "tool" {
			return agent.Response{Content: json.RawMessage(`{"appointment_ids":[]}`)}, nil
		}

		return agent.Response{ToolCalls: []agent.ToolCall{{
			ID: "query", Name: "list_appointments", Arguments: json.RawMessage(`{}`),
		}}}, nil
	})
	core := &application.Service{Repo: repository{}, Location: time.UTC}
	assistant := agent.New(core, model)
	assistant.Traces = trace.NewJSONL(&output, trace.Options{CaptureSyntheticContent: true})
	requestID := uuid.NewString()
	ctx := trace.WithRequestID(context.Background(), requestID)

	result, err := assistant.Reply(ctx, domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
	if err != nil {
		t.Fatal(err)
	}

	var execution trace.Execution
	if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
		t.Fatalf("expected a complete JSON execution: %v", err)
	}

	if execution.TraceID == "" || execution.RequestID != requestID ||
		execution.ConversationID != result.ConversationID || execution.Outcome != "ok" {
		t.Fatalf("missing execution correlation: %+v", execution)
	}

	wantKinds := []string{
		"conversation.acquire", "conversation.save", "model", "tool", "model", "answer", "conversation.save",
	}
	if len(execution.Steps) != len(wantKinds) {
		t.Fatalf("expected model/tool/model/answer, got %+v", execution.Steps)
	}

	for i, step := range execution.Steps {
		if step.ID == "" || step.Order != i+1 || step.Kind != wantKinds[i] ||
			step.StartedAt.IsZero() || step.DurationNS < 0 || step.Outcome != "ok" {
			t.Fatalf("incomplete or unordered step %d: %+v", i, step)
		}
	}

	tool := execution.Steps[3]
	if tool.ParentID != execution.Steps[2].ID || tool.ToolName != "list_appointments" || tool.CallID == "" ||
		string(tool.Input) != `{}` || string(tool.Output) != `[]` {
		t.Fatalf("tool call is not linked to its model request/result: %+v", tool)
	}

	if !bytes.Contains(execution.Input, []byte("Meus agendamentos")) ||
		!bytes.Contains(execution.Output, []byte(result.Message)) ||
		!bytes.Contains(execution.Steps[4].Input, []byte("ToolCallID")) ||
		!bytes.Contains(execution.Steps[4].Output, []byte("appointment_ids")) {
		t.Fatal("synthetic trace lost input, tool continuation, structured answer or final reply")
	}
}

func TestTraceLocatesConversationAccessFailure(t *testing.T) {
	var output bytes.Buffer

	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		t.Fatal("unknown conversation must be rejected before calling the model")

		return agent.Response{}, nil
	})
	assistant := agent.New(&application.Service{}, model)
	assistant.Traces = trace.NewJSONL(&output, trace.Options{})

	_, err := assistant.ReplyConversation(
		context.Background(), domain.Principal{PatientID: uuid.New()}, uuid.New(), "Contato",
	)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unexpected error: %v", err)
	}

	var execution trace.Execution
	if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}

	if len(execution.Steps) != 1 || execution.Steps[0].Kind != "conversation.acquire" ||
		execution.FailureStepID != execution.Steps[0].ID || execution.Steps[0].ErrorCode != "NOT_FOUND" {
		t.Fatalf("lost the conversation failure before model execution: %+v", execution)
	}
}

func TestTraceKeepsConcurrentExecutionsDistinct(t *testing.T) {
	var output bytes.Buffer

	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{ToolCalls: []agent.ToolCall{{
			ID: "shared-call-id", Name: "offer_human_contact", Arguments: json.RawMessage(`{"reason":"out_of_scope"}`),
		}}}, nil
	})
	assistant := agent.New(&application.Service{}, model)
	assistant.Traces = trace.NewJSONL(&output, trace.Options{})

	const count = 16

	var workers sync.WaitGroup

	for range count {
		workers.Go(func() {
			_, err := assistant.Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Contato")
			if err != nil {
				t.Error(err)
			}
		})
	}

	workers.Wait()

	decoder := json.NewDecoder(&output)
	traces, requests, conversations := map[string]bool{}, map[string]bool{}, map[uuid.UUID]bool{}

	for range count {
		var execution trace.Execution
		if err := decoder.Decode(&execution); err != nil {
			t.Fatalf("concurrent trace writes interleaved: %v", err)
		}

		if execution.Outcome != "ok" || traces[execution.TraceID] || requests[execution.RequestID] ||
			conversations[execution.ConversationID] {
			t.Fatalf("concurrent executions shared identity: %+v", execution)
		}

		traces[execution.TraceID] = true
		requests[execution.RequestID] = true
		conversations[execution.ConversationID] = true
	}

	if err := decoder.Decode(new(trace.Execution)); err != io.EOF {
		t.Fatalf("expected exactly %d executions: %v", count, err)
	}
}

type unavailableTraceWriter struct{}

type panickingTraceRepository struct{ application.Repository }

func (panickingTraceRepository) Appointments(
	context.Context, uuid.UUID, application.AppointmentFilter,
) ([]domain.Appointment, error) {
	panic("private panic credential")
}

func TestTraceRecordsPanicsWithoutChangingTheirPropagation(t *testing.T) {
	for _, kind := range []string{"model", "tool"} {
		t.Run(kind, func(t *testing.T) {
			var output bytes.Buffer

			model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
				if kind == "model" {
					panic("private panic credential")
				}

				return agent.Response{ToolCalls: []agent.ToolCall{{
					ID: "query", Name: "list_appointments", Arguments: json.RawMessage(`{}`),
				}}}, nil
			})
			assistant := agent.New(&application.Service{Repo: panickingTraceRepository{}, Location: time.UTC}, model)
			assistant.Traces = trace.NewJSONL(&output, trace.Options{CaptureSyntheticContent: true})

			var recovered any

			func() {
				defer func() { recovered = recover() }()

				_, _ = assistant.Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Consulta")
			}()

			if recovered != "private panic credential" {
				t.Fatalf("instrumentation changed panic propagation: %v", recovered)
			}

			var execution trace.Execution
			if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
				t.Fatal(err)
			}

			failed := execution.Steps[len(execution.Steps)-1]
			if execution.Outcome != "error" || execution.ErrorCode != "INTERNAL_ERROR" ||
				failed.Kind != kind || failed.Outcome != "error" || failed.ID != execution.FailureStepID {
				t.Fatalf("panic outcome=%q code=%q step=%q/%q", execution.Outcome, execution.ErrorCode,
					failed.Kind, failed.Outcome)
			}

			for _, step := range execution.Steps {
				if step.Outcome == "" {
					t.Fatal("panic left an unfinished step")
				}
			}

			if bytes.Contains(output.Bytes(), []byte("private panic credential")) {
				t.Fatal("panic diagnostic appeared in trace")
			}

			m := execution.Metrics
			if m.ModelCalls != 1 || m.ApplicationErrors != 1 || m.MissingUsageCalls != 1 ||
				m.UsageStatus != "unavailable" || m.Cost.Amount != nil ||
				(kind == "tool" && (m.ToolCalls != 1 || m.ToolFailures != 1)) {
				t.Fatalf("panic lost effort counters or invented usage: %+v", m)
			}
		})
	}
}

func (unavailableTraceWriter) Write([]byte) (int, error) {
	return 0, errors.New("trace disk unavailable")
}

func TestTraceExporterFailurePreservesReplyAndConversation(t *testing.T) {
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{ToolCalls: []agent.ToolCall{{
			ID: "contact", Name: "offer_human_contact", Arguments: json.RawMessage(`{"reason":"out_of_scope"}`),
		}}}, nil
	})
	principal := domain.Principal{PatientID: uuid.New()}
	assistant := agent.New(&application.Service{}, model)
	assistant.Traces = trace.NewJSONL(unavailableTraceWriter{}, trace.Options{CaptureSyntheticContent: true})

	first, err := assistant.Reply(context.Background(), principal, "Quero contato humano")
	if err != nil || first.ConversationID == uuid.Nil || first.Message == "" || assistant.Traces.Failures() != 1 {
		t.Fatalf("export failure changed the reply: %+v, %v", first, err)
	}

	var output bytes.Buffer

	assistant.Traces = trace.NewJSONL(&output, trace.Options{})

	followup, err := assistant.ReplyConversation(
		context.Background(), principal, first.ConversationID, "Quero contato humano",
	)
	if err != nil || followup.ConversationID != first.ConversationID || followup.Message != first.Message {
		t.Fatalf("export failure changed saved conversation state: %+v, %v", followup, err)
	}

	_, err = assistant.ReplyConversation(context.Background(), principal, first.ConversationID, "Contato")
	if err != nil {
		t.Fatal(err)
	}

	decoder := json.NewDecoder(&output)

	var previous trace.Execution

	for range 2 {
		var execution trace.Execution
		if err := decoder.Decode(&execution); err != nil {
			t.Fatal(err)
		}

		if execution.TraceID == previous.TraceID || execution.RequestID == previous.RequestID ||
			execution.ConversationID != first.ConversationID {
			t.Fatalf("follow-up did not receive a distinct execution in the same conversation: %+v", execution)
		}

		previous = execution
	}

	if err := decoder.Decode(new(trace.Execution)); err != io.EOF {
		t.Fatalf("expected exactly one JSONL record per turn: %v", err)
	}
}

func TestTraceIdentifiesFailedToolsInvalidResponsesCancellationAndLimits(t *testing.T) {
	for _, test := range []struct {
		name, kind, code, outcome string
		model                     modelFunc
	}{
		{
			name: "tool", kind: "tool", code: "MODEL_INVALID_RESPONSE", outcome: "error",
			model: func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{ToolCalls: []agent.ToolCall{{
					ID: "bad-args", Name: "list_appointments", Arguments: json.RawMessage(`{"patient_id":"forged"}`),
				}}}, nil
			},
		},
		{
			name: "invalid response", kind: "validation", code: "MODEL_INVALID_RESPONSE", outcome: "error",
			model: func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{ToolCalls: []agent.ToolCall{
					{ID: "one", Name: "list_appointments", Arguments: json.RawMessage(`{}`)},
					{ID: "two", Name: "list_appointments", Arguments: json.RawMessage(`{}`)},
				}}, nil
			},
		},
		{
			name: "cancelled", kind: "model", code: "CANCELLED", outcome: "cancelled",
			model: func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{}, context.Canceled
			},
		},
		{
			name: "deadline", kind: "model", code: "DEADLINE_EXCEEDED", outcome: "timeout",
			model: func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{}, context.DeadlineExceeded
			},
		},
		{
			name: "limit", kind: "limit", code: "AGENT_STEP_LIMIT", outcome: "limit",
			model: func(_ context.Context, in agent.Request) (agent.Response, error) {
				return agent.Response{ToolCalls: []agent.ToolCall{{
					ID: fmt.Sprintf("query-%d", len(in.Messages)), Name: "list_appointments",
					Arguments: json.RawMessage(`{}`),
				}}}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer

			assistant := agent.New(&application.Service{Repo: repository{}, Location: time.UTC}, test.model)
			assistant.Traces = trace.NewJSONL(&output, trace.Options{})

			_, err := assistant.Reply(
				context.Background(),
				domain.Principal{PatientID: uuid.New()},
				"Meus agendamentos",
			)
			if err == nil {
				t.Fatal("expected a failed turn")
			}

			var execution trace.Execution
			if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
				t.Fatal(err)
			}

			if execution.Outcome != test.outcome || execution.ErrorCode != test.code || len(execution.Steps) == 0 {
				t.Fatalf("wrong execution outcome: %+v", execution)
			}

			failed := execution.Steps[len(execution.Steps)-1]
			if failed.ID != execution.FailureStepID || failed.Kind != test.kind || failed.ErrorCode != test.code ||
				failed.Outcome != test.outcome {
				t.Fatalf("wrong failure origin: %+v", failed)
			}
		})
	}
}

func TestTraceLocatesModelFailureAfterTool(t *testing.T) {
	var output bytes.Buffer

	want := errors.New("private provider diagnostic")
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		if in.Messages[len(in.Messages)-1].Role == "tool" {
			return agent.Response{}, want
		}

		return agent.Response{ToolCalls: []agent.ToolCall{{
			ID: "query", Name: "list_appointments", Arguments: json.RawMessage(`{}`),
		}}}, nil
	})
	assistant := agent.New(&application.Service{Repo: repository{}, Location: time.UTC}, model)
	assistant.Traces = trace.NewJSONL(&output, trace.Options{CaptureSyntheticContent: true})

	_, err := assistant.Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
	if !errors.Is(err, want) {
		t.Fatalf("tracing changed the original error: %v", err)
	}

	var execution trace.Execution
	if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}

	if len(execution.Steps) != 5 || execution.Outcome != "error" ||
		execution.FailureStepID != execution.Steps[4].ID || execution.Steps[4].Kind != "model" ||
		execution.Steps[4].Outcome != "error" || execution.Steps[4].ErrorCode != "INTERNAL_ERROR" {
		t.Fatalf("cannot navigate to the originating model failure: %+v", execution)
	}

	if bytes.Contains(output.Bytes(), []byte("private provider diagnostic")) {
		t.Fatal("trace exposed a raw provider error")
	}
}

func TestMetricsIncludeUsageBeforeModelFailureAndFailedTools(t *testing.T) {
	for _, failure := range []string{"none", "model", "tool"} {
		t.Run(failure, func(t *testing.T) {
			var output bytes.Buffer

			now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
			model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
				now = now.Add(100 * time.Millisecond)

				if in.Messages[len(in.Messages)-1].Role == "tool" {
					if failure == "model" {
						return agent.Response{}, errors.New("private credential")
					}

					return agent.Response{Content: json.RawMessage(`{"appointment_ids":[]}`),
						Usage: agent.ModelUsage{Provider: "test", Model: "model",
							Tokens: &agent.TokenUsage{Input: 50, Output: 10}}}, nil
				}

				args := json.RawMessage(`{}`)
				if failure == "tool" {
					args = json.RawMessage(`{"patient_id":"forged"}`)
				}

				return agent.Response{ToolCalls: []agent.ToolCall{{
					ID: "query", Name: "list_appointments", Arguments: args,
				}}, Usage: agent.ModelUsage{Provider: "test", Model: "model",
					Tokens: &agent.TokenUsage{Input: 100, Output: 20}}}, nil
			})
			assistant := agent.New(&application.Service{Repo: repository{}, Location: time.UTC}, model)
			assistant.Traces = trace.NewJSONL(&output, trace.Options{Now: func() time.Time { return now }})

			_, err := assistant.Reply(
				context.Background(),
				domain.Principal{PatientID: uuid.New()},
				"Meus atendimentos",
			)
			if (err != nil) != (failure != "none") {
				t.Fatalf("unexpected reply error: %v", err)
			}

			var execution trace.Execution
			if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
				t.Fatal(err)
			}

			m := execution.Metrics
			calls, failedTools, appErrors, input, outputTokens := 2, 0, 0, int64(150), int64(30)
			status, missing := "complete", 0

			if failure != "none" {
				appErrors, input, outputTokens = 1, 100, 20
			}

			if failure == "tool" {
				calls, failedTools = 1, 1
			}

			if failure == "model" {
				status, missing = "partial", 1
			}

			if m.ModelCalls != calls || m.ToolCalls != 1 || m.ToolFailures != failedTools ||
				m.ApplicationErrors != appErrors || m.Steps != len(execution.Steps) ||
				m.InputTokens == nil || *m.InputTokens != input || m.OutputTokens == nil ||
				*m.OutputTokens != outputTokens || m.UsageStatus != status || m.MissingUsageCalls != missing {
				t.Fatalf("lost partial effort/consumption: %+v", m)
			}

			if m.ModelDurationNS != int64(calls)*100000000 || m.TotalDurationNS != m.ModelDurationNS ||
				m.Cost.Status != "unavailable" || m.Cost.Amount != nil || m.Cost.MissingPriceCalls != calls {
				t.Fatalf("incorrect timing or invented price: %+v", m)
			}

			if failure != "none" && execution.FailureStepID == "" {
				t.Fatal("failed metrics lost trace correlation")
			}
		})
	}
}
