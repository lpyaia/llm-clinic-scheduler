package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type modelFunc func(context.Context, agent.Request) (agent.Response, error)

func (f modelFunc) Complete(ctx context.Context, in agent.Request) (agent.Response, error) {
	return f(ctx, in)
}

func TestMalformedToolArgumentsNeverReachSchedulingCore(t *testing.T) {
	for _, raw := range []string{
		`{"status":"scheduled","status":"cancelled"}`,
		`{"Status":"scheduled"}`,
		`{"patient_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"status":"invented"}`, `{"from":"yesterday"}`, `{"from":"2026-10-27T00:00:00Z","to":"2026-10-26T00:00:00Z"}`,
		`null`, `[]`, `{`, `{} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			core := &application.Service{} // Any repository access panics: invalid calls must stop before it.
			model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "list_appointments", Arguments: json.RawMessage(raw)}}}, nil
			})

			_, err := agent.New(core, model).Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("expected rejected arguments, got %v", err)
			}
		})
	}
}

func TestEmptyListIsReportedFaithfully(t *testing.T) {
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		if in.Messages[len(in.Messages)-1].Role == "user" {
			return agent.Response{ToolCalls: []agent.ToolCall{{ID: "empty", Name: "list_appointments", Arguments: json.RawMessage(`{}`)}}}, nil
		}

		if in.Messages[len(in.Messages)-1].Content != "[]" {
			t.Fatal("empty tool result must be an array")
		}

		return agent.Response{Content: json.RawMessage(`{"appointment_ids":[]}`)}, nil
	})
	core := &application.Service{Repo: repository{}}

	out, err := agent.New(core, model).Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Tenho algo marcado?")
	if err != nil || !strings.Contains(out.Message, "não tem atendimentos") || len(out.Appointments) != 0 {
		t.Fatalf("result=%+v error=%v", out, err)
	}
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

		return agent.Response{ToolCalls: []agent.ToolCall{{ID: fmt.Sprintf("call-%d", step), Name: "list_appointments", Arguments: json.RawMessage(`{}`)}}}, nil
	})
	core := &application.Service{Repo: repository{}}

	_, err := agent.New(core, model).Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
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

		_, err := agent.New(&application.Service{}, model).Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
		if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
			t.Fatalf("response=%+v error=%v", response, err)
		}
	}
}

func TestFabricatedOrMalformedFinalAnswersAreRejected(t *testing.T) {
	for _, raw := range []string{`{"appointment_ids":["invented"]}`, `{"appointment_ids":null}`, `{}`, `null`, `{"appointment_ids":[],"message":"Inventado"}`, `{"appointment_ids":[]} {}`} {
		model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
			if in.Messages[len(in.Messages)-1].Role == "user" {
				return agent.Response{ToolCalls: []agent.ToolCall{{ID: "query", Name: "list_appointments", Arguments: json.RawMessage(`{}`)}}}, nil
			}

			return agent.Response{Content: json.RawMessage(raw)}, nil
		})

		_, err := agent.New(&application.Service{Repo: repository{}}, model).Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
		if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
			t.Fatalf("answer=%s error=%v", raw, err)
		}
	}
}

func TestCancellationAfterModelReplyPreventsToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		cancel()

		return agent.Response{ToolCalls: []agent.ToolCall{{ID: "query", Name: "list_appointments", Arguments: json.RawMessage(`{}`)}}}, nil
	})

	_, err := agent.New(&application.Service{}, model).Reply(ctx, domain.Principal{PatientID: uuid.New()}, "Meus agendamentos")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation before executing tool: %v", err)
	}
}

type repository struct {
	application.Repository
	rows []domain.Appointment
}

func (r repository) Appointments(_ context.Context, patientID uuid.UUID, filter application.AppointmentFilter) ([]domain.Appointment, error) {
	var out []domain.Appointment

	for _, row := range r.rows {
		if row.PatientID == patientID && (filter.Status == "" || row.Status == filter.Status) {
			out = append(out, row)
		}
	}

	return out, nil
}

func TestPatientListsAppointmentsInPortuguese(t *testing.T) {
	patientID := uuid.New()
	appointmentID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	loc := time.FixedZone("clinic", -3*60*60)
	core := &application.Service{Location: loc, Repo: repository{rows: []domain.Appointment{
		{ID: appointmentID, PatientID: patientID, DoctorName: "Dra. Ana", Category: "consultation", Status: domain.Scheduled, StartsAt: time.Date(2026, 10, 26, 17, 0, 0, 0, time.UTC)},
		{ID: uuid.New(), PatientID: uuid.New(), DoctorName: "Outro paciente", Status: domain.Scheduled},
	}}}
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			return agent.Response{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "list_appointments", Arguments: json.RawMessage(`{}`)}}}, nil
		}

		if last.Role != "tool" || !strings.Contains(last.Content, "Dra. Ana") || strings.Contains(last.Content, "Outro paciente") || strings.Contains(last.Content, patientID.String()) {
			t.Fatalf("tool result must contain only this patient's appointment data: %+v", last)
		}

		return agent.Response{Content: json.RawMessage(`{"appointment_ids":["11111111-1111-4111-8111-111111111111"]}`)}, nil
	})

	out, err := agent.New(core, model).Reply(context.Background(), domain.Principal{PatientID: patientID}, "Quais atendimentos tenho marcados?")
	if err != nil {
		t.Fatal(err)
	}

	for _, text := range []string{"Dra. Ana", "26/10/2026 às 14:00", "consulta", "agendado"} {
		if !strings.Contains(out.Message, text) {
			t.Fatalf("reply missing %q: %s", text, out.Message)
		}
	}

	if len(out.Appointments) != 1 || out.Appointments[0].ID != appointmentID {
		t.Fatalf("unexpected appointments: %+v", out)
	}
}
