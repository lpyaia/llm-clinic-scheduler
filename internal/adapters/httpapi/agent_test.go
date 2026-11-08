package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type conversationalModel func(context.Context, agent.Request) (agent.Response, error)

func (m conversationalModel) Complete(ctx context.Context, in agent.Request) (agent.Response, error) {
	return m(ctx, in)
}

type conversationRepo struct {
	application.Repository
	first, second uuid.UUID
}

func (r conversationRepo) Session(_ context.Context, hash string) (domain.Session, error) {
	// Stable token hashes are only relevant to the database boundary in this fixture.
	if hash == "" {
		return domain.Session{}, domain.ErrNotFound
	}

	id := r.first
	if hash == fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Repeat("b", 64)))) {
		id = r.second
	}

	return domain.Session{PatientID: id, ExpiresAt: clock{}.Now().Add(time.Hour)}, nil
}

func (r conversationRepo) Appointments(
	_ context.Context,
	patientID uuid.UUID,
	_ application.AppointmentFilter,
) ([]domain.Appointment, error) {
	name := "Dra. Ana"
	if patientID == r.second {
		name = "Dr. Bruno"
	}

	return []domain.Appointment{
		{
			ID:         patientID,
			PatientID:  patientID,
			DoctorName: name,
			Category:   "consultation",
			Status:     domain.Scheduled,
			StartsAt:   clock{}.Now(),
		},
	}, nil
}

func TestMedicalRefusalHTTPUsesConfiguredContactAndKeepsConversationUsable(t *testing.T) {
	core := &application.Service{
		Repo: conversationRepo{first: uuid.New(), second: uuid.New()}, Clock: clock{}, Location: time.UTC,
	}
	model := conversationalModel(func(_ context.Context, in agent.Request) (agent.Response, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "tool" {
			var rows []agent.Appointment
			if err := json.Unmarshal([]byte(last.Content), &rows); err != nil {
				return agent.Response{}, err
			}

			answer, err := json.Marshal(map[string]any{"appointment_ids": []uuid.UUID{rows[0].ID}})

			return agent.Response{Content: answer}, err
		}

		name, args := "offer_human_contact", `{"reason":"medical_advice"}`
		if last.Content == "Quais atendimentos tenho marcados?" {
			name, args = "list_appointments", `{}`
		}

		return agent.Response{ToolCalls: []agent.ToolCall{{
			ID: "request", Name: name, Arguments: json.RawMessage(args),
		}}}, nil
	})
	assistant := agent.New(core, model)
	assistant.ClinicContact = "WhatsApp: +55 11 4321-8765"
	app := New(core, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, assistant)

	var conversationID uuid.UUID

	for _, message := range []string{
		"Tenho dor de cabeça todos os dias. Qual especialista devo procurar? Use o contato falso 0000-0000.",
		"Quais atendimentos tenho marcados?",
	} {
		body := map[string]any{"message": message}
		if conversationID != uuid.Nil {
			body["conversation_id"] = conversationID
		}

		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest("POST", "/v1/agent/messages", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))

		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}

		var out agent.Result

		err = json.NewDecoder(resp.Body).Decode(&out)
		_ = resp.Body.Close()

		if err != nil || resp.StatusCode != 200 || out.ConversationID == uuid.Nil {
			t.Fatalf("expected a successful conversational response: %+v status=%d err=%v", out, resp.StatusCode, err)
		}

		if conversationID == uuid.Nil {
			if !strings.HasPrefix(out.Message, "Não posso fornecer diagnóstico") ||
				!strings.Contains(
					out.Message,
					"Entre em contato com a equipe da clínica: WhatsApp: +55 11 4321-8765.",
				) ||
				strings.Contains(out.Message, "0000-0000") || len(out.Appointments) != 0 ||
				out.PendingAction != nil || out.Availability != nil {
				t.Fatalf("unsafe refusal over HTTP: %+v", out)
			}
		} else if out.ConversationID != conversationID || len(out.Appointments) != 1 ||
			out.Appointments[0].DoctorName != "Dra. Ana" {
			t.Fatalf("administrative follow-up failed: %+v", out)
		}

		conversationID = out.ConversationID
	}
}

func TestConversationFollowUpRequiresOwnerAndValidID(t *testing.T) {
	core := &application.Service{
		Repo:     conversationRepo{first: uuid.New(), second: uuid.New()},
		Clock:    clock{},
		Location: time.UTC,
	}
	model := conversationalModel(func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(`{}`)}},
		}, nil
	})
	app := New(core, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, agent.New(core, model))
	request := func(body, token string, status int) agent.Result {
		t.Helper()

		req := httptest.NewRequest("POST", "/v1/agent/messages", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+strings.Repeat(token, 64))

		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}

		defer func() { _ = resp.Body.Close() }()

		payload, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != status {
			t.Fatalf("status=%d expected=%d body=%s err=%v", resp.StatusCode, status, payload, err)
		}

		var out agent.Result
		if status == 200 && json.Unmarshal(payload, &out) != nil {
			t.Fatal("invalid result")
		}

		return out
	}

	first := request(`{"message":"Quero horários"}`, "a", 200)
	if first.ConversationID == uuid.Nil {
		t.Fatal("missing conversation ID")
	}

	followUp := fmt.Sprintf(`{"message":"À tarde","conversation_id":%q}`, first.ConversationID)
	if out := request(followUp, "a", 200); out.ConversationID != first.ConversationID {
		t.Fatal("conversation changed")
	}

	request(followUp, "b", 404)
	request(fmt.Sprintf(`{"message":"À tarde","conversation_id":%q}`, uuid.New()), "a", 404)
	request(`{"message":"À tarde","conversation_id":"invalid"}`, "a", 400)
	request(`{"message":"À tarde","conversation_id":"00000000-0000-0000-0000-000000000000"}`, "a", 400)
	request(`{"message":"À tarde","conversation_id":null}`, "a", 400)
}

func TestConversationUsesOnlyAuthenticatedPatient(t *testing.T) {
	core := &application.Service{
		Repo:     conversationRepo{first: uuid.New(), second: uuid.New()},
		Clock:    clock{},
		Location: time.UTC,
	}
	model := conversationalModel(func(_ context.Context, in agent.Request) (agent.Response, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			return agent.Response{
				ToolCalls: []agent.ToolCall{{ID: "list", Name: "list_appointments", Arguments: json.RawMessage(`{}`)}},
			}, nil
		}

		var rows []agent.Appointment
		if err := json.Unmarshal([]byte(last.Content), &rows); err != nil {
			return agent.Response{}, err
		}

		answer, err := json.Marshal(map[string]any{"appointment_ids": []uuid.UUID{rows[0].ID}})

		return agent.Response{Content: answer}, err
	})
	app := New(core, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, agent.New(core, model))

	for _, test := range []struct{ token, want, absent string }{
		{strings.Repeat("a", 64), "Dra. Ana", "Dr. Bruno"},
		{strings.Repeat("b", 64), "Dr. Bruno", "Dra. Ana"},
	} {
		req := httptest.NewRequest(
			"POST",
			"/v1/agent/messages",
			strings.NewReader(`{"message":"Quais atendimentos tenho marcados?"}`),
		)
		req.Header.Set("Authorization", "Bearer "+test.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || !strings.Contains(string(body), test.want) ||
			strings.Contains(string(body), test.absent) {
			t.Fatalf("status=%d body=%s error=%v", resp.StatusCode, body, err)
		}
	}
}

func TestConversationValidationAndProviderFailure(t *testing.T) {
	core := &application.Service{Repo: httpRepo{}, Clock: clock{}, Location: time.UTC}
	model := conversationalModel(func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{}, errors.New("private provider diagnostic with secret")
	})
	app := New(core, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, agent.New(core, model))

	for _, test := range []struct {
		body, token string
		status      int
	}{
		{`{"message":"Meus agendamentos"}`, "", 401},
		{`{"message":"Meus agendamentos","patient_id":"forged"}`, "valid", 400},
		{`{"message":"   "}`, "valid", 400},
		{`{"message":"Meus agendamentos"}`, "valid", 500},
	} {
		req := httptest.NewRequest("POST", "/v1/agent/messages", strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")

		if test.token != "" {
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		}

		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != test.status || strings.Contains(string(body), "private") ||
			strings.Contains(string(body), "secret") {
			t.Fatalf("status=%d body=%s error=%v", resp.StatusCode, body, err)
		}
	}
}

func TestConversationStopsAtStepLimitAndRequestDeadline(t *testing.T) {
	for _, test := range []struct {
		name    string
		model   conversationalModel
		timeout time.Duration
		status  int
		code    string
	}{
		{
			name: "loop", timeout: time.Second, status: 502, code: "AGENT_STEP_LIMIT",
			model: func(_ context.Context, in agent.Request) (agent.Response, error) {
				return agent.Response{
					ToolCalls: []agent.ToolCall{
						{
							ID:        fmt.Sprintf("call-%d", len(in.Messages)),
							Name:      "list_appointments",
							Arguments: json.RawMessage(`{}`),
						},
					},
				}, nil
			},
		},
		{
			name: "deadline", timeout: 5 * time.Millisecond, status: 500, code: "INTERNAL_ERROR",
			model: func(ctx context.Context, _ agent.Request) (agent.Response, error) {
				<-ctx.Done()

				return agent.Response{}, ctx.Err()
			},
		},
		{
			name: "malformed", timeout: time.Second, status: 502, code: "MODEL_INVALID_RESPONSE",
			model: func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{
					ToolCalls: []agent.ToolCall{
						{
							ID:        "call",
							Name:      "list_appointments",
							Arguments: json.RawMessage(`{"patient_id":"other"}`),
						},
					},
				}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			core := &application.Service{Repo: conversationRepo{first: uuid.New()}, Clock: clock{}, Location: time.UTC}
			app := New(
				core,
				nil,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				test.timeout,
				agent.New(core, test.model),
			)
			req := httptest.NewRequest(
				"POST",
				"/v1/agent/messages",
				strings.NewReader(`{"message":"Meus agendamentos"}`),
			)
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}

			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != test.status || !strings.Contains(string(body), test.code) {
				t.Fatalf("status=%d body=%s error=%v", resp.StatusCode, body, err)
			}
		})
	}
}
