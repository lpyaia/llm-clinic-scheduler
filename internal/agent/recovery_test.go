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

func TestUnavailableBookingExplainsHowToRecover(t *testing.T) {
	a, core, repo, p, proposal, next := preparedBooking(t)
	repo.rows = append(repo.rows, domain.Appointment{
		ID: uuid.New(), PatientID: uuid.New(), SlotID: repo.slots[0].ID, Status: domain.Scheduled,
	})
	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Confirmo")
	if domain.ErrorCode(err) != "SLOT_UNAVAILABLE" || !strings.Contains(err.Error(), "nova busca") ||
		out.PendingAction != nil || len(out.Appointments) != 0 {
		t.Fatalf("missing safe recovery guidance: %+v err=%v", out, err)
	}

	assertBookings(t, core, p, 0)
}

func TestEmptyAvailabilityReportsWindowAndAsksForDifferentCriteria(t *testing.T) {
	core, repo := bookingFixture()
	repo.slots = nil
	p := domain.Principal{PatientID: uuid.New()}
	a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return toolReply("search", "find_available_slots", `{"doctor":"Ana"}`), nil
	}))

	out, err := a.ReplyConversation(context.Background(), p, uuid.Nil, "Procuro a Ana")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 0 ||
		!out.Availability.Expanded || !strings.Contains(out.Message, "Não encontrei horários") ||
		!strings.Contains(out.Message, "critérios") ||
		!strings.Contains(out.Message, out.Availability.Until.Format("02/01/2006")) {
		t.Fatalf("missing empty-window recovery guidance: %+v err=%v", out, err)
	}
}

func TestChangedOrExpiredProposalNeedsNewPreparationAndConfirmation(t *testing.T) {
	for _, code := range []string{"ACTION_EXPIRED", "APPOINTMENT_CHANGED"} {
		t.Run(code, func(t *testing.T) {
			a, core, repo, p, _, proposal, next := preparedRescheduling(t)
			if code == "ACTION_EXPIRED" {
				core.Clock = searchClock{proposal.PendingAction.ExpiresAt}
			} else {
				repo.rows[0].Version++
			}

			original := assertBookings(t, core, p, 1)[0]
			*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)
			ctx := context.Background()

			_, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo")
			if domain.ErrorCode(err) != code || !strings.Contains(err.Error(), "novo resumo") {
				t.Fatalf("stale proposal needs fresh consent: %v", err)
			}

			_, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo outra vez")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("stale proposal remained confirmable: %v", err)
			}

			for _, call := range []struct{ name, args string }{
				{"select_appointment", `{}`},
				{"find_available_slots", `{}`},
				{"reschedule_appointment", fmt.Sprintf(`{"slot_id":%q}`, repo.slots[1].ID)},
			} {
				*next = toolReply(call.name, call.name, call.args)

				out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Prepare novamente")
				if err != nil {
					t.Fatal(err)
				}

				if call.name == "reschedule_appointment" &&
					(out.PendingAction == nil || out.PendingAction.ID == proposal.PendingAction.ID) {
					t.Fatalf("new preparation must present a new proposal: %+v", out)
				}
			}

			if rows := assertBookings(t, core, p, 1); rows[0] != original {
				t.Fatalf("new preparation mutated the original: %+v", rows)
			}

			*next = toolReply("new-confirm", "respond_to_proposal", `{"decision":"confirm"}`)

			out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo o novo resumo")
			if err != nil || len(out.Appointments) != 1 {
				t.Fatalf("new confirmation failed: %+v err=%v", out, err)
			}

			rows := assertBookings(t, core, p, 1)
			if rows[0].ID != original.ID || rows[0].SlotID != repo.slots[1].ID ||
				rows[0].Category != original.Category || rows[0].Version != original.Version+1 {
				t.Fatalf("recovery changed the wrong reservation: %+v", rows)
			}
		})
	}
}

func TestWeeklyLimitRecoveryOffersAnotherWeekWithoutBypassingTheLimit(t *testing.T) {
	a, core, repo, p, proposal, next := preparedBooking(t)
	for range 3 {
		repo.rows = append(repo.rows, domain.Appointment{
			ID: uuid.New(), PatientID: p.PatientID, SlotID: uuid.New(),
			StartsAt: repo.slots[0].StartsAt, Status: domain.Scheduled,
		})
	}

	ctx := context.Background()
	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	_, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo")
	if domain.ErrorCode(err) != "WEEKLY_LIMIT_REACHED" ||
		!strings.Contains(err.Error(), "três agendamentos") || !strings.Contains(err.Error(), "outra semana") {
		t.Fatalf("missing permitted recovery path: %v", err)
	}

	assertBookings(t, core, p, 3)

	other := repo.slots[0]
	other.ID, other.StartsAt = uuid.New(), other.StartsAt.AddDate(0, 0, 7)
	repo.slots = append(repo.slots, other)
	*next = toolReply("search", "find_available_slots", `{"from":"2026-11-09T00:00:00-03:00"}`)

	if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Busque em outra semana"); err != nil {
		t.Fatal(err)
	}

	args := fmt.Sprintf(`{"slot_id":%q,"category":"consultation"}`, other.ID)
	*next = toolReply("prepare", "create_appointment", args)

	out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Quero esse horário")
	if err != nil || out.PendingAction == nil {
		t.Fatalf("another week must allow a new proposal: %+v err=%v", out, err)
	}

	assertBookings(t, core, p, 3)

	*next = toolReply("new-confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo o novo resumo"); err != nil {
		t.Fatal(err)
	}

	assertBookings(t, core, p, 4)
}

func TestModelFailuresReturnSafeGuidanceAndAResumableConversation(t *testing.T) {
	for _, scenario := range []string{"provider", "timeout", "step limit"} {
		t.Run(scenario, func(t *testing.T) {
			core, _ := bookingFixture()
			p := domain.Principal{PatientID: uuid.New()}
			failing, step := true, 0
			model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
				if !failing {
					return toolReply("search", "find_available_slots", `{}`), nil
				}

				switch scenario {
				case "provider":
					return agent.Response{}, errors.New("private provider details")
				case "timeout":
					return agent.Response{}, context.DeadlineExceeded
				default:
					step++

					return toolReply(fmt.Sprintf("list-%d", step), "list_appointments", `{}`), nil
				}
			})
			a := agent.New(core, model)
			out, err := a.ReplyConversation(context.Background(), p, uuid.Nil, "Meus agendamentos")

			var failure *agent.TurnError
			if !errors.As(err, &failure) || failure.ConversationID == uuid.Nil ||
				strings.Contains(err.Error(), "private provider") || !strings.Contains(err.Error(), "confirmação") ||
				out.PendingAction != nil || len(out.Appointments) != 0 {
				t.Fatalf("failure must be safe and resumable: %+v err=%v", out, err)
			}

			if scenario == "step limit" && step != agent.MaxSteps {
				t.Fatalf("unexpected model-call budget: %d", step)
			}

			failing = false

			out, err = a.ReplyConversation(context.Background(), p, failure.ConversationID, "Vamos buscar horários")
			if err != nil || out.ConversationID != failure.ConversationID || out.PendingAction != nil {
				t.Fatalf("failed conversation could not resume: %+v err=%v", out, err)
			}

			assertBookings(t, core, p, 0)
		})
	}
}

// confirmationFault models a lost commit acknowledgement at the database boundary.
type confirmationFault struct {
	application.Repository
	commitErr   error
	lookupErr   error
	afterCommit func()
}

func (r *confirmationFault) Transaction(ctx context.Context, fn func(application.Repository) error) error {
	if err := r.Repository.Transaction(ctx, fn); err != nil {
		return err
	}

	if r.afterCommit != nil {
		r.afterCommit()
	}

	return r.commitErr
}

type recoveryStore struct {
	agent.ConversationStore
	rejectCompleted bool
	rejectUncertain bool
}

func (s *recoveryStore) Acquire(
	ctx context.Context, patient, id uuid.UUID, access agent.ConversationAccess,
) (agent.LockedConversation, error) {
	locked, err := s.ConversationStore.Acquire(ctx, patient, id, access)
	if err != nil {
		return nil, err
	}

	return &recoveryLock{LockedConversation: locked, store: s}, nil
}

type recoveryLock struct {
	agent.LockedConversation
	store *recoveryStore
}

func (l *recoveryLock) Save(ctx context.Context, state agent.Conversation) error {
	if (l.store.rejectCompleted && state.Completed != nil) || (l.store.rejectUncertain && state.Uncertain != nil) {
		return errors.New("conversation save unavailable")
	}

	return l.LockedConversation.Save(ctx, state)
}

func TestConfirmationSurvivesInterruptedFinalSave(t *testing.T) {
	for _, scenario := range []string{"save failure", "cancelled after commit", "checkpoint failure"} {
		t.Run(scenario, func(t *testing.T) {
			core, repo := bookingFixture()
			p := domain.Principal{PatientID: uuid.New()}
			store := &recoveryStore{ConversationStore: agent.NewMemoryConversations()}
			next := toolReply("search", "find_available_slots", `{"doctor":"Ana"}`)
			a := agent.NewWithConversations(
				core,
				modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
					return next, nil
				}),
				store,
			)
			ctx := context.Background()

			first, err := a.ReplyConversation(ctx, p, uuid.Nil, "Ana")
			if err != nil {
				t.Fatal(err)
			}

			next = toolReply("prepare", "create_appointment",
				fmt.Sprintf(`{"slot_id":%q,"category":"exam"}`, repo.slots[0].ID))

			if _, err := a.ReplyConversation(ctx, p, first.ConversationID, "Primeiro, exame"); err != nil {
				t.Fatal(err)
			}

			confirmCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			switch scenario {
			case "save failure":
				store.rejectCompleted = true
			case "checkpoint failure":
				store.rejectUncertain = true
			case "cancelled after commit":
				core.Repo = &confirmationFault{Repository: repo, afterCommit: cancel}
			}

			next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)

			out, err := a.ReplyConversation(confirmCtx, p, first.ConversationID, "Confirmo")
			if err == nil || out.Message != "" || len(out.Appointments) != 0 {
				t.Fatalf("interrupted save must not report success: %+v err=%v", out, err)
			}

			store.rejectCompleted, store.rejectUncertain = false, false

			if scenario == "checkpoint failure" {
				assertBookings(t, core, p, 0)

				_, err = a.ReplyConversation(ctx, p, first.ConversationID, "Confirmo outra vez")
				if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
					t.Fatalf("failed checkpoint retained authority: %v", err)
				}

				return
			}

			before := assertBookings(t, core, p, 1)[0]
			next = toolReply("unrelated", "create_appointment", `{}`)

			out, err = a.ReplyConversation(ctx, p, first.ConversationID, "O que aconteceu?")
			if err != nil || len(out.Appointments) != 1 || out.Appointments[0].ID != before.ID {
				t.Fatalf("lost committed result after interrupted save: %+v err=%v", out, err)
			}

			if after := assertBookings(t, core, p, 1)[0]; after != before {
				t.Fatalf("recovery repeated confirmation: %+v", after)
			}
		})
	}
}

func TestRolledBackReschedulingCanPrepareAndConfirmAgain(t *testing.T) {
	a, core, repo, p, original, proposal, next := preparedRescheduling(t)
	repo.updateErr = errors.New("write failed after changing appointment")
	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)
	ctx := context.Background()

	if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo"); !errors.Is(err, repo.updateErr) {
		t.Fatalf("expected write rollback: %v", err)
	}

	_, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirme outra vez")
	if domain.ErrorCode(err) != "ACTION_NOT_COMPLETED" {
		t.Fatalf("pending outcome must require a new proposal: %v", err)
	}

	if rows := assertBookings(t, core, p, 1); rows[0] != original {
		t.Fatalf("failed confirmation lost original reservation: %+v", rows)
	}

	repo.updateErr = nil
	*next = toolReply("prepare", "reschedule_appointment", fmt.Sprintf(`{"slot_id":%q}`, repo.slots[1].ID))

	out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Prepare outro resumo")
	if err != nil || out.PendingAction == nil || out.PendingAction.ID == proposal.PendingAction.ID {
		t.Fatalf("new preparation did not replace failed attempt: %+v err=%v", out, err)
	}

	*next = toolReply("new-confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo o novo resumo"); err != nil {
		t.Fatal(err)
	}

	rows := assertBookings(t, core, p, 1)
	if rows[0].ID != original.ID || rows[0].SlotID != repo.slots[1].ID || rows[0].Version != 2 {
		t.Fatalf("unexpected recovered reservation: %+v", rows)
	}
}

func (r *confirmationFault) Action(ctx context.Context, id, patient uuid.UUID, lock bool) (domain.Action, error) {
	if r.lookupErr != nil {
		return domain.Action{}, r.lookupErr
	}

	return r.Repository.Action(ctx, id, patient, lock)
}

func TestUncertainConfirmationIsQueriedBeforeAnotherMutation(t *testing.T) {
	for _, operation := range []string{"create", "reschedule", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			var (
				a        *agent.Agent
				core     *application.Service
				p        domain.Principal
				proposal agent.Result
				next     *agent.Response
			)

			switch operation {
			case "create":
				a, core, _, p, proposal, next = preparedBooking(t)
			case "reschedule":
				a, core, _, p, _, proposal, next = preparedRescheduling(t)
			case "cancel":
				a, core, _, p, _, proposal, next = preparedCancellation(t)
			}

			fault := &confirmationFault{Repository: core.Repo, commitErr: errors.New("lost commit acknowledgement")}
			core.Repo = fault
			*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)
			ctx := context.Background()

			_, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo")
			if !errors.Is(err, fault.commitErr) {
				t.Fatalf("expected uncertain result: %v", err)
			}

			before := assertBookings(t, core, p, 1)[0]
			fault.lookupErr = errors.New("action lookup unavailable")

			_, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo novamente")
			if !errors.Is(err, fault.lookupErr) {
				t.Fatalf("must check uncertain action before any retry: %v", err)
			}

			fault.lookupErr = nil
			// Even a new intent must first resolve the previous uncertain mutation.
			*next = toolReply("other", "cancel_appointment", `{}`)

			out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Quero outra operação")
			if err != nil || len(out.Appointments) != 1 || out.Appointments[0].ID != before.ID ||
				!strings.Contains(out.Message, "confirmad") || out.PendingAction != nil {
				t.Fatalf("must recover confirmed result from the core: %+v err=%v", out, err)
			}

			if after := assertBookings(t, core, p, 1)[0]; after != before {
				t.Fatalf("recovery repeated the mutation: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestUncertainFailedActionDiscardsUnavailableChoices(t *testing.T) {
	a, core, repo, p, original, proposal, next := preparedRescheduling(t)
	competitor := original
	competitor.ID, competitor.PatientID, competitor.SlotID = uuid.New(), uuid.New(), repo.slots[1].ID
	repo.rows = append(repo.rows, competitor)
	fault := &confirmationFault{Repository: core.Repo, commitErr: errors.New("lost failure acknowledgement")}
	core.Repo = fault
	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)
	ctx := context.Background()

	if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo"); !errors.Is(err, fault.commitErr) {
		t.Fatalf("expected uncertain failure: %v", err)
	}

	_, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Qual foi o resultado?")
	if domain.ErrorCode(err) != "SLOT_UNAVAILABLE" {
		t.Fatalf("must recover the recorded business failure: %v", err)
	}

	*next = toolReply("old-choice", "reschedule_appointment", fmt.Sprintf(`{"slot_id":%q}`, repo.slots[1].ID))

	out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Tente o mesmo horário")
	if err != nil || out.PendingAction != nil || !strings.Contains(out.Message, "busca") {
		t.Fatalf("unavailable choice survived recovery: %+v err=%v", out, err)
	}

	if rows := assertBookings(t, core, p, 1); rows[0] != original {
		t.Fatalf("recovery lost the original reservation: %+v", rows)
	}
}
