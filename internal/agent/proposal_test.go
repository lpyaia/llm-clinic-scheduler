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

func TestProposalNaturalConfirmationsUseTheModel(t *testing.T) {
	for _, message := range []string{"Aham, pode marcar", "confirmado", "aceito"} {
		t.Run(message, func(t *testing.T) {
			a, core, _, p, proposal, next := preparedBooking(t)
			*next = toolReply("decision", "respond_to_proposal", `{"decision":"confirm"}`)

			out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, message)
			if err != nil || len(out.Appointments) != 1 {
				t.Fatalf("contextual confirmation=%+v err=%v", out, err)
			}

			rows := assertBookings(t, core, p, 1)
			if rows[0].ID != out.Appointments[0].ID || out.PendingAction != nil {
				t.Fatalf("unexpected confirmation: %+v", out)
			}
		})
	}
}

func TestProposalRejectionAndClarificationRevokeAuthority(t *testing.T) {
	for _, test := range []struct{ message, decision string }{
		{"Melhor deixar para depois", "reject"},
		{"Pode ser, se não chover", "clarify"},
		{"É com a Ana?", "clarify"},
		{"talvez", "clarify"},
	} {
		t.Run(test.message, func(t *testing.T) {
			a, core, _, p, proposal, next := preparedBooking(t)
			args := fmt.Sprintf(`{"decision":%q}`, test.decision)
			*next = toolReply("decision", "respond_to_proposal", args)

			out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, test.message)
			if err != nil || out.PendingAction != nil || len(out.Appointments) != 0 || out.Message == "" {
				t.Fatalf("non-confirmation=%+v err=%v", out, err)
			}

			*next = toolReply("stale-confirm", "respond_to_proposal", `{"decision":"confirm"}`)

			_, err = a.ReplyConversation(context.Background(), p, proposal.ConversationID, "aceito")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("revoked proposal accepted a decision: %v", err)
			}

			assertBookings(t, core, p, 0)
		})
	}
}

func TestProposalInvalidDecisionsRevokeAuthority(t *testing.T) {
	for _, args := range []string{
		`{}`, `null`, `[]`, `{`, `{} {}`, `{"decision":null}`, `{"decision":1}`,
		`{"decision":"change"}`, `{"decision":"CONFIRM"}`, `{"Decision":"confirm"}`,
		`{"decision":"confirm","decision":"reject"}`,
		`{"decision":"confirm","action_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"decision":"confirm","patient_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"decision":"confirm","revision":1}`,
	} {
		t.Run(args, func(t *testing.T) {
			a, core, _, p, proposal, next := preparedBooking(t)
			*next = toolReply("invalid", "respond_to_proposal", args)

			_, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "aceito")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("invalid decision accepted: %v", err)
			}

			*next = toolReply("stale", "respond_to_proposal", `{"decision":"confirm"}`)

			_, err = a.ReplyConversation(context.Background(), p, proposal.ConversationID, "confirmado")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("invalid response preserved authority: %v", err)
			}

			assertBookings(t, core, p, 0)
		})
	}
}

// The mutable model is an external provider boundary. Setup uses the real public
// conversation API; tests replace only the provider's next completion behavior.
func proposalWithModel(t *testing.T) (
	*agent.Agent, *application.Service, domain.Principal, agent.Result, *modelFunc,
) {
	t.Helper()

	core, repo := bookingFixture()
	p := domain.Principal{PatientID: uuid.New()}
	next := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return toolReply("search", "find_available_slots", `{"doctor":"Ana"}`), nil
	})
	a := agent.New(core, modelFunc(func(ctx context.Context, in agent.Request) (agent.Response, error) {
		return next(ctx, in)
	}))

	first, err := a.ReplyConversation(context.Background(), p, uuid.Nil, "Procuro a Ana")
	if err != nil {
		t.Fatal(err)
	}

	next = func(context.Context, agent.Request) (agent.Response, error) {
		args := fmt.Sprintf(`{"slot_id":%q,"category":"consultation"}`, repo.slots[0].ID)
		return toolReply("prepare", "create_appointment", args), nil
	}

	proposal, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Primeiro, consulta")
	if err != nil || proposal.PendingAction == nil {
		t.Fatalf("proposal=%+v err=%v", proposal, err)
	}

	return a, core, p, proposal, &next
}

func proposalToolAvailable(in agent.Request) bool {
	for _, tool := range in.Tools {
		if tool.Name == "respond_to_proposal" {
			return true
		}
	}

	return false
}

func TestProposalInterpretationFailuresConsumeAuthority(t *testing.T) {
	for _, failure := range []string{"provider", "cancelled", "final answer", "multiple tools", "mixed content"} {
		t.Run(failure, func(t *testing.T) {
			a, core, p, proposal, next := proposalWithModel(t)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			*next = func(_ context.Context, in agent.Request) (agent.Response, error) {
				if !proposalToolAvailable(in) ||
					!strings.Contains(in.Messages[0].Content, proposal.PendingAction.Summary) ||
					in.Messages[len(in.Messages)-1].Content != "aceito" {
					t.Fatal("interpretation needs the presented proposal and current message")
				}

				out := toolReply("decision", "respond_to_proposal", `{"decision":"confirm"}`)

				switch failure {
				case "provider":
					return agent.Response{}, errors.New("provider unavailable")
				case "cancelled":
					cancel()
				case "final answer":
					return appointmentAnswer(t), nil
				case "multiple tools":
					out.ToolCalls = append(out.ToolCalls, out.ToolCalls[0])
				case "mixed content":
					out.Content = json.RawMessage(`{"appointment_ids":[]}`)
				}

				return out, nil
			}

			if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "aceito"); err == nil {
				t.Fatal("expected interpretation failure")
			}

			*next = func(_ context.Context, in agent.Request) (agent.Response, error) {
				if proposalToolAvailable(in) {
					t.Fatal("failed interpretation preserved the response tool")
				}

				return toolReply("stale", "respond_to_proposal", `{"decision":"confirm"}`), nil
			}

			_, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "confirmado")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("failed interpretation preserved authority: %v", err)
			}

			assertBookings(t, core, p, 0)
		})
	}
}

func TestProposalIntentDiversionCannotConfirmLaterInTheLoop(t *testing.T) {
	a, core, p, proposal, next := proposalWithModel(t)
	*next = func(_ context.Context, in agent.Request) (agent.Response, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			return toolReply("list", "list_appointments", `{}`), nil
		}

		if proposalToolAvailable(in) {
			t.Fatal("intent diversion retained the proposal response tool")
		}

		return toolReply("stale", "respond_to_proposal", `{"decision":"confirm"}`), nil
	}

	_, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Antes, liste meus agendamentos")
	if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
		t.Fatalf("intent diversion allowed confirmation: %v", err)
	}

	assertBookings(t, core, p, 0)
}

func TestProposalCannotBePreparedAndConfirmedInOneTurn(t *testing.T) {
	for _, together := range []bool{false, true} {
		t.Run(fmt.Sprintf("same completion=%t", together), func(t *testing.T) {
			a, core, p, proposal, next := proposalWithModel(t)
			*next = func(_ context.Context, in agent.Request) (agent.Response, error) {
				last := in.Messages[len(in.Messages)-1]
				if last.Role == "user" && !together {
					return toolReply("list", "list_appointments", `{}`), nil
				}

				out := toolReply("prepare", "create_appointment", `{"category":"exam"}`)
				if together {
					confirm := toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)
					out.ToolCalls = append(out.ToolCalls, confirm.ToolCalls...)
				}

				return out, nil
			}

			out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Prefiro exame, confirmo")
			if together {
				if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
					t.Fatalf("combined preparation and confirmation accepted: %v", err)
				}
			} else if err != nil || out.PendingAction == nil || out.PendingAction.ID == proposal.PendingAction.ID {
				t.Fatalf("multi-step preparation needs a new summary: %+v err=%v", out, err)
			}

			assertBookings(t, core, p, 0)
		})
	}
}

func TestProposalExpirationDuringInterpretationIsRevalidatedByCore(t *testing.T) {
	a, core, p, proposal, next := proposalWithModel(t)
	*next = func(context.Context, agent.Request) (agent.Response, error) {
		core.Clock = searchClock{proposal.PendingAction.ExpiresAt.Add(time.Second)}
		return toolReply("late", "respond_to_proposal", `{"decision":"confirm"}`), nil
	}

	_, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "aceito")
	if domain.ErrorCode(err) != "ACTION_EXPIRED" {
		t.Fatalf("core did not revalidate expiration: %v", err)
	}

	assertBookings(t, core, p, 0)
}

func TestProposalResponseUnavailableWithoutProposal(t *testing.T) {
	for _, message := range []string{"não", "aceito"} {
		t.Run(message, func(t *testing.T) {
			core, _ := bookingFixture()
			p := domain.Principal{PatientID: uuid.New()}
			a := agent.New(core, modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
				if proposalToolAvailable(in) {
					t.Fatal("no proposal should expose no response tool")
				}

				return toolReply("clarify", "create_appointment", `{}`), nil
			}))

			out, err := a.ReplyConversation(context.Background(), p, uuid.Nil, message)
			if err != nil || out.PendingAction != nil || strings.Contains(out.Message, "descartada") {
				t.Fatalf("message without proposal=%+v err=%v", out, err)
			}

			assertBookings(t, core, p, 0)
		})
	}
}

func TestProposalForgedResponseWithoutProposalCannotReserve(t *testing.T) {
	core, _ := bookingFixture()
	p := domain.Principal{PatientID: uuid.New()}
	a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return toolReply("forged", "respond_to_proposal", `{"decision":"confirm"}`), nil
	}))

	_, err := a.ReplyConversation(context.Background(), p, uuid.Nil, "aceito")
	if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
		t.Fatalf("response without proposal accepted: %v", err)
	}

	assertBookings(t, core, p, 0)
}

func TestProposalCannotUseAnActionOwnedByAnotherPatient(t *testing.T) {
	core, repo := bookingFixture()
	p, other := domain.Principal{PatientID: uuid.New()}, domain.Principal{PatientID: uuid.New()}
	ctx := context.Background()

	action, err := core.Prepare(ctx, other, application.PrepareInput{
		Operation: domain.CreateAppointment, SlotID: repo.slots[0].ID, Category: "consultation",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Seed a corrupted reference through the store's public contract, as a
	// future persistence adapter could return; the core must still guard ownership.
	store := agent.NewMemoryConversations()
	id := uuid.New()

	locked, err := store.Acquire(ctx, p.PatientID, id, agent.ConversationAccess{CreateConversation: true})
	if err != nil {
		t.Fatal(err)
	}

	err = locked.Save(ctx, agent.Conversation{Proposal: &agent.PendingAction{ID: action.ID}, Revision: 1})
	locked.Release()

	if err != nil {
		t.Fatal(err)
	}

	a := agent.NewWithConversations(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		t.Fatal("foreign reference must be rejected before interpretation")
		return agent.Response{}, nil
	}), store)

	_, err = a.ReplyConversation(ctx, p, id, "aceito")
	if domain.ErrorCode(err) != "NOT_FOUND" {
		t.Fatalf("foreign action was not hidden: %v", err)
	}

	assertBookings(t, core, p, 0)
	assertBookings(t, core, other, 0)
}
