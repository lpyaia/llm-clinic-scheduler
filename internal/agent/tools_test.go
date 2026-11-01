package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func toolReply(id, name, args string) agent.Response {
	return agent.Response{
		ToolCalls: []agent.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(args)}},
	}
}

func appointmentAnswer(t *testing.T, ids ...uuid.UUID) agent.Response {
	t.Helper()

	content, err := json.Marshal(map[string]any{"appointment_ids": append([]uuid.UUID{}, ids...)})
	if err != nil {
		t.Fatal(err)
	}

	return agent.Response{Content: content}
}

func TestFinalAnswerUsesOnlyLatestAppointmentQuery(t *testing.T) {
	patientID := uuid.New()
	firstID, secondID, thirdID := uuid.New(), uuid.New(), uuid.New()

	for _, test := range []struct {
		name, status string
		ids          []uuid.UUID
		invalid      bool
	}{
		{"latest results", domain.Cancelled, []uuid.UUID{secondID, thirdID}, false},
		{"earlier results", domain.Cancelled, []uuid.UUID{firstID}, true},
		{"reordered results", domain.Cancelled, []uuid.UUID{thirdID, secondID}, true},
		{"latest empty result", domain.Completed, []uuid.UUID{}, false},
		{"earlier nonempty result after empty", domain.Completed, []uuid.UUID{firstID}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			core := &application.Service{Location: time.UTC, Repo: repository{rows: []domain.Appointment{
				{ID: firstID, PatientID: patientID, Status: domain.Scheduled},
				{ID: secondID, PatientID: patientID, Status: domain.Cancelled},
				{ID: thirdID, PatientID: patientID, Status: domain.Cancelled},
			}}}
			step := 0
			model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
				step++
				if step == 1 {
					return toolReply("first", "list_appointments", `{}`), nil
				}

				last, previous := in.Messages[len(in.Messages)-1], in.Messages[len(in.Messages)-2]

				wantCallID := "first"
				if step == 3 {
					wantCallID = "second"
				}

				if last.Role != "tool" || last.ToolCallID != wantCallID || previous.Role != "assistant" ||
					len(previous.ToolCalls) != 1 || previous.ToolCalls[0].ID != wantCallID {
					t.Fatalf("tool transcript lost call correlation: %+v", in.Messages)
				}

				var rows []agent.Appointment
				if err := json.Unmarshal([]byte(last.Content), &rows); err != nil || rows == nil {
					t.Fatalf("tool data must remain a JSON array: %s err=%v", last.Content, err)
				}

				if step == 2 {
					if len(rows) != 1 || rows[0].ID != firstID {
						t.Fatalf("unexpected first query: %+v", rows)
					}

					return toolReply("second", "list_appointments", fmt.Sprintf(`{"status":%q}`, test.status)), nil
				}

				if step != 3 {
					t.Fatal("unexpected completion after final answer")
				}

				if test.status == domain.Completed && len(rows) != 0 {
					t.Fatalf("empty query retained earlier rows: %+v", rows)
				}

				return appointmentAnswer(t, test.ids...), nil
			})

			out, err := agent.New(core, model).Reply(
				context.Background(), domain.Principal{PatientID: patientID}, "Meus agendamentos",
			)
			if test.invalid {
				if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
					t.Fatalf("expected rejected final answer, got %+v err=%v", out, err)
				}

				return
			}

			if err != nil || len(out.Appointments) != len(test.ids) || out.Appointments == nil || step != 3 {
				t.Fatalf("result=%+v err=%v completions=%d", out, err, step)
			}

			for i, id := range test.ids {
				if out.Appointments[i].ID != id {
					t.Fatalf("final answer differs from latest query: %+v", out)
				}
			}
		})
	}
}

func TestAppointmentFinalizerAndCallIDsAreScopedToOneMessage(t *testing.T) {
	core := &application.Service{Repo: repository{}}
	step := 0
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		step++
		if step == 1 || step == 4 {
			return toolReply("list", "list_appointments", `{}`), nil
		}

		if step == 5 {
			for _, message := range in.Messages {
				if message.Content == "Repita sem consultar" {
					t.Fatal("unverified answer was added to conversation history")
				}
			}
		}

		return appointmentAnswer(t), nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	first, err := a.Reply(context.Background(), p, "Meus agendamentos")
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.ReplyConversation(context.Background(), p, first.ConversationID, "Repita sem consultar")
	if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
		t.Fatalf("follow-up reused the previous finalizer: %v", err)
	}

	out, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Consulte novamente")
	if err != nil || out.ConversationID != first.ConversationID || step != 5 {
		t.Fatalf("new message could not reuse call ID: %+v err=%v completions=%d", out, err, step)
	}
}

func TestRepeatedCallIDsAreRejectedAcrossTools(t *testing.T) {
	for _, name := range []string{"list_appointments", "find_available_slots"} {
		t.Run(name, func(t *testing.T) {
			core := &application.Service{Repo: repository{}}
			step := 0
			model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
				step++
				tool := "list_appointments"
				if step > 1 {
					tool = name
				}

				return toolReply("repeated", tool, `{}`), nil
			})

			_, err := agent.New(core, model).Reply(
				context.Background(), domain.Principal{PatientID: uuid.New()}, "Buscar",
			)
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" || step != 2 {
				t.Fatalf("repeated ID accepted: err=%v completions=%d", err, step)
			}
		})
	}
}

type combinedRepo struct {
	*catalogRepo
	appointments repository
}

func (r combinedRepo) Appointments(
	ctx context.Context,
	patientID uuid.UUID,
	filter application.AppointmentFilter,
) ([]domain.Appointment, error) {
	return r.appointments.Appointments(ctx, patientID, filter)
}

func TestSlotSearchRepliesWithoutAnotherModelCompletion(t *testing.T) {
	for _, test := range []struct {
		name, args, question string
		listFirst            bool
	}{
		{"availability", `{"doctor":"Ana","date":"next_week"}`, "", false},
		{"clarification", `{}`, "Com qual médico", false},
		{"listing then availability", `{"doctor":"Ana","date":"next_week"}`, "", true},
		{"listing then clarification", `{}`, "Com qual médico", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, catalog := searchFixture()
			p := domain.Principal{PatientID: uuid.New()}
			core.Repo = combinedRepo{catalogRepo: catalog, appointments: repository{rows: []domain.Appointment{
				{ID: uuid.New(), PatientID: p.PatientID, Status: domain.Scheduled, StartsAt: core.Clock.Now()},
			}}}
			step := 0
			model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
				step++
				if test.listFirst && step == 1 {
					return toolReply("list", "list_appointments", `{}`), nil
				}

				if test.listFirst {
					last := in.Messages[len(in.Messages)-1]
					if last.Role != "tool" || last.ToolCallID != "list" {
						t.Fatalf("missing listing result before search: %+v", in.Messages)
					}
				}

				return toolReply("search", "find_available_slots", test.args), nil
			})

			out, err := agent.New(core, model).Reply(context.Background(), p, "Buscar horários")

			wantSteps := 1
			if test.listFirst {
				wantSteps++
			}

			if err != nil || step != wantSteps || out.Appointments == nil || len(out.Appointments) != 0 {
				t.Fatalf("search did not finish the message: %+v err=%v completions=%d", out, err, step)
			}

			if test.question != "" {
				if out.Availability != nil || !strings.Contains(out.Message, test.question) {
					t.Fatalf("unexpected clarification: %+v", out)
				}
			} else if out.Availability == nil || len(out.Availability.Slots) != 1 ||
				out.Availability.Slots[0].ID != catalog.slots[0].ID {
				t.Fatalf("unexpected availability: %+v", out)
			}
		})
	}
}
