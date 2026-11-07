package agent_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func TestCancellationRequiresLaterConfirmationAndReplaysTheSameResult(t *testing.T) {
	core, repo, p, original := reschedulingFixture()
	second := original
	second.ID, second.SlotID, second.StartsAt = uuid.New(), repo.slots[1].ID, repo.slots[1].StartsAt
	repo.rows = append(repo.rows, second)
	next := toolReply("prepare", "cancel_appointment", `{"doctor":"Ana","to":"2026-11-05T00:00:00-03:00"}`)
	a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return next, nil
	}))
	ctx := context.Background()

	proposal, err := a.ReplyConversation(ctx, p, uuid.Nil, "Cancele meu exame de quarta com a Ana, confirmo")
	if err != nil || proposal.PendingAction == nil {
		t.Fatalf("expected cancellation proposal: %+v err=%v", proposal, err)
	}

	for _, text := range []string{"Cancelar", "04/11/2026 às 14:00", "Ana Souza", "exame", "confirma o cancelamento"} {
		if !strings.Contains(proposal.Message, text) {
			t.Fatalf("proposal missing %q: %s", text, proposal.Message)
		}
	}

	if rows := assertBookings(t, core, p, 2); rows[0] != original || rows[1] != second {
		t.Fatalf("preparation changed appointments: %+v", rows)
	}

	action, err := core.GetAction(ctx, p, proposal.PendingAction.ID)
	if err != nil || action.Operation != domain.CancelAppointment || action.AppointmentID != original.ID {
		t.Fatalf("proposal must bind cancellation to original: %+v err=%v", action, err)
	}

	next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	for attempt := range 2 {
		if attempt == 1 {
			core.Clock = searchClock{proposal.PendingAction.ExpiresAt}
		}

		out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Sim, confirmo o cancelamento")
		if err != nil || out.PendingAction != nil || len(out.Appointments) != 1 ||
			out.Appointments[0].ID != original.ID || out.Appointments[0].Status != domain.Cancelled ||
			!strings.Contains(out.Message, "Cancelamento confirmado") {
			t.Fatalf("confirmation=%+v err=%v", out, err)
		}

		want := original
		want.Status, want.Version = domain.Cancelled, 2

		if rows := assertBookings(t, core, p, 2); rows[0] != want || rows[1] != second {
			t.Fatalf("confirmation changed another appointment or replayed mutation: %+v", rows)
		}
	}
}

func preparedCancellation(t *testing.T) (
	*agent.Agent, *application.Service, *reschedulingRepo, domain.Principal,
	domain.Appointment, agent.Result, *agent.Response,
) {
	t.Helper()

	core, repo, p, original := reschedulingFixture()
	next := toolReply("prepare", "cancel_appointment", `{}`)
	a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return next, nil
	}))

	proposal, err := a.ReplyConversation(context.Background(), p, uuid.Nil, "Quero cancelar meu exame")
	if err != nil || proposal.PendingAction == nil {
		t.Fatalf("expected cancellation proposal: %+v err=%v", proposal, err)
	}

	return a, core, repo, p, original, proposal, &next
}

func TestCancellationRefusalAndAmbiguityPreserveAppointmentAndRevokeProposal(t *testing.T) {
	for _, decision := range []string{"reject", "clarify"} {
		t.Run(decision, func(t *testing.T) {
			a, core, _, p, original, proposal, next := preparedCancellation(t)
			*next = toolReply("decision", "respond_to_proposal", fmt.Sprintf(`{"decision":%q}`, decision))
			ctx := context.Background()

			out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Prefiro deixar para depois")
			if err != nil || out.PendingAction != nil ||
				!strings.Contains(
					strings.ToLower(out.Message),
					"cancelamento",
				) || !strings.Contains(out.Message, "mantido") {
				t.Fatalf("non-confirmation must explain preservation: %+v err=%v", out, err)
			}

			*next = toolReply("stale", "respond_to_proposal", `{"decision":"confirm"}`)

			_, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("discarded proposal accepted confirmation: %v", err)
			}

			if rows := assertBookings(t, core, p, 1); rows[0] != original {
				t.Fatalf("non-confirmation changed appointment: %+v", rows)
			}
		})
	}
}

func TestCancellationClarifiesAmbiguousOwnAppointmentsBeforePreparing(t *testing.T) {
	core, repo, p, original := reschedulingFixture()
	second := original
	second.ID, second.SlotID, second.StartsAt = uuid.New(), repo.slots[1].ID, repo.slots[1].StartsAt
	foreign := original
	foreign.ID, foreign.PatientID = uuid.New(), uuid.New()
	repo.rows = append(repo.rows, second, foreign)
	step := 0
	a := agent.New(core, modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		step++
		args := `{}`

		if step == 2 {
			if !strings.Contains(in.Messages[0].Content, second.ID.String()) ||
				strings.Contains(in.Messages[0].Content, foreign.ID.String()) {
				t.Fatal("follow-up must expose only verified own candidates")
			}

			args = fmt.Sprintf(`{"appointment_id":%q}`, second.ID)
		}

		return toolReply("choose", "cancel_appointment", args), nil
	}))
	ctx := context.Background()

	out, err := a.ReplyConversation(ctx, p, uuid.Nil, "Quero cancelar minha consulta")
	if err != nil || out.PendingAction != nil || len(out.Appointments) != 2 ||
		!strings.Contains(out.Message, "Qual agendamento") {
		t.Fatalf("expected own candidates and clarification: %+v err=%v", out, err)
	}

	out, err = a.ReplyConversation(ctx, p, out.ConversationID, "O de quinta")
	if err != nil || out.PendingAction == nil || !strings.Contains(out.Message, "05/11/2026 às 14:00") {
		t.Fatalf("expected proposal for chosen candidate: %+v err=%v", out, err)
	}

	action, err := core.GetAction(ctx, p, out.PendingAction.ID)
	if err != nil || action.AppointmentID != second.ID {
		t.Fatalf("proposal targeted wrong appointment: %+v err=%v", action, err)
	}

	if rows := assertBookings(t, core, p, 2); rows[0] != original || rows[1] != second {
		t.Fatalf("selection changed appointments: %+v", rows)
	}
}

func TestCancellationFiltersOwnershipEligibilityAndAmbiguousSelection(t *testing.T) {
	for _, scenario := range []string{
		"foreign", "started", "cancelled", "completed", "unknown ID", "ambiguous ID", "category", "doctor", "dates",
	} {
		t.Run(scenario, func(t *testing.T) {
			core, repo, p, original := reschedulingFixture()
			args := `{}`

			switch scenario {
			case "foreign":
				repo.rows[0].PatientID = uuid.New()
				args = fmt.Sprintf(`{"appointment_id":%q}`, original.ID)
			case "started":
				repo.rows[0].StartsAt = core.Clock.Now()
			case "cancelled":
				repo.rows[0].Status = domain.Cancelled
			case "completed":
				repo.rows[0].Status = domain.Completed
			case "unknown ID":
				args = fmt.Sprintf(`{"appointment_id":%q}`, uuid.New())
			case "ambiguous ID":
				args = `{"appointment_id":""}`
			case "category":
				args = `{"category":"consultation"}`
			case "doctor":
				args = `{"doctor":"Bruno"}`
			case "dates":
				args = `{"from":"2026-11-05T00:00:00-03:00","to":"2026-11-06T00:00:00-03:00"}`
			}

			a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
				return toolReply("choose", "cancel_appointment", args), nil
			}))
			before := repo.rows[0]
			ctx := context.Background()

			out, err := a.ReplyConversation(ctx, p, uuid.Nil, "Cancele meu atendimento")
			if err != nil || out.PendingAction != nil || !strings.Contains(out.Message, "Qual agendamento") {
				t.Fatalf("unsafe or ambiguous selection prepared: %+v err=%v", out, err)
			}

			wantCandidates := 0
			if scenario == "ambiguous ID" {
				wantCandidates = 1
			}

			if len(out.Appointments) != wantCandidates {
				t.Fatalf("unexpected candidates: %+v", out.Appointments)
			}

			owner := domain.Principal{PatientID: before.PatientID}
			if rows := assertBookings(t, core, owner, 1); rows[0] != before {
				t.Fatalf("selection changed appointment: %+v", rows)
			}
		})
	}
}

func TestCancellationChangingTargetRequiresAnotherSummary(t *testing.T) {
	a, core, repo, p, original, first, next := preparedCancellation(t)
	second := original
	second.ID, second.SlotID, second.StartsAt = uuid.New(), repo.slots[1].ID, repo.slots[1].StartsAt
	repo.rows = append(repo.rows, second)
	*next = toolReply("change", "cancel_appointment", fmt.Sprintf(`{"appointment_id":%q}`, second.ID))
	ctx := context.Background()

	proposal, err := a.ReplyConversation(ctx, p, first.ConversationID, "Sim, mas cancele o de quinta")
	if err != nil || proposal.PendingAction == nil || proposal.PendingAction.ID == first.PendingAction.ID ||
		!strings.Contains(proposal.Message, "05/11/2026 às 14:00") {
		t.Fatalf("changed target requires a fresh summary: %+v err=%v", proposal, err)
	}

	if rows := assertBookings(t, core, p, 2); rows[0] != original || rows[1] != second {
		t.Fatalf("changing the target cancelled an appointment: %+v", rows)
	}

	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	out, err := a.ReplyConversation(ctx, p, first.ConversationID, "Confirmo esse cancelamento")
	if err != nil || len(out.Appointments) != 1 || out.Appointments[0].ID != second.ID ||
		out.Appointments[0].Status != domain.Cancelled {
		t.Fatalf("confirmation must target the replacement proposal: %+v err=%v", out, err)
	}

	second.Status, second.Version = domain.Cancelled, 2
	if rows := assertBookings(t, core, p, 2); rows[0] != original || rows[1] != second {
		t.Fatalf("confirmation used stale authority: %+v", rows)
	}
}

func TestCancellationDoesNotRestoreACompletedCancellationOnRefusal(t *testing.T) {
	a, core, _, p, original, proposal, next := preparedCancellation(t)
	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)
	ctx := context.Background()

	if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo"); err != nil {
		t.Fatal(err)
	}

	*next = toolReply("reject", "respond_to_proposal", `{"decision":"reject"}`)

	out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Não, melhor deixar")
	if err != nil || !strings.Contains(out.Message, "cancelamento já realizado foi mantido") {
		t.Fatalf("refusal must describe completed cancellation accurately: %+v err=%v", out, err)
	}

	original.Status, original.Version = domain.Cancelled, 2
	if rows := assertBookings(t, core, p, 1); rows[0] != original {
		t.Fatalf("refusal restored a completed cancellation: %+v", rows)
	}
}

func TestCancellationRejectsInvalidArgumentsWithoutPreparing(t *testing.T) {
	for _, args := range []string{
		`{"appointment_id":"00000000-0000-0000-0000-000000000000"}`,
		`{"appointment_id":"invalid"}`,
		`{"patient_id":"someone-else"}`,
		`{"action_id":"another-action"}`,
		`{"appointment_id":null,"appointment_id":null}`,
		`{"category":"invalid"}`,
	} {
		t.Run(args, func(t *testing.T) {
			core, _, p, original := reschedulingFixture()
			a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
				return toolReply("invalid", "cancel_appointment", args), nil
			}))

			out, err := a.ReplyConversation(context.Background(), p, uuid.Nil, "Cancele meu exame")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" || out.PendingAction != nil {
				t.Fatalf("invalid arguments accepted: %+v err=%v", out, err)
			}

			if rows := assertBookings(t, core, p, 1); rows[0] != original {
				t.Fatalf("invalid arguments changed appointment: %+v", rows)
			}
		})
	}
}

func TestCancellationConfirmationFailuresNeverReportSuccess(t *testing.T) {
	for _, scenario := range []string{"expired", "changed", "started", "cancelled", "foreign", "write failure"} {
		t.Run(scenario, func(t *testing.T) {
			a, core, repo, p, _, proposal, next := preparedCancellation(t)
			wantCode := "APPOINTMENT_CHANGED"

			switch scenario {
			case "expired":
				core.Clock = searchClock{proposal.PendingAction.ExpiresAt}
				wantCode = "ACTION_EXPIRED"
			case "changed":
				repo.rows[0].Version++
			case "started":
				core.Clock = searchClock{repo.rows[0].StartsAt}
				// Keep the action unexpired to exercise eligibility revalidation at confirmation.
				action := repo.actions[proposal.PendingAction.ID]
				action.ExpiresAt = core.Clock.Now().AddDate(0, 0, 1)
				repo.actions[action.ID] = action
				wantCode = "APPOINTMENT_NOT_MUTABLE"
			case "cancelled":
				repo.rows[0].Status = domain.Cancelled
				wantCode = "APPOINTMENT_NOT_MUTABLE"
			case "foreign":
				repo.rows[0].PatientID = uuid.New()
				wantCode = "NOT_FOUND"
			case "write failure":
				repo.updateErr = errors.New("write failed after updating appointment")
				wantCode = "INTERNAL_ERROR"
			}

			before := repo.rows[0]
			*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)
			ctx := context.Background()

			out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo o cancelamento")
			if domain.ErrorCode(err) != wantCode || out.Message != "" || len(out.Appointments) != 0 {
				t.Fatalf("failure reported success: %+v err=%v; want %s", out, err, wantCode)
			}

			owner := domain.Principal{PatientID: before.PatientID}
			if rows := assertBookings(t, core, owner, 1); rows[0] != before {
				t.Fatalf("failed confirmation changed appointment: %+v", rows)
			}

			_, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo novamente")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("failed confirmation retained authority: %v", err)
			}
		})
	}
}
