package agent_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type bookingRepo struct {
	*catalogRepo
	actions map[uuid.UUID]domain.Action
	rows    []domain.Appointment
}

func (r *bookingRepo) Transaction(_ context.Context, fn func(application.Repository) error) error {
	actions, rows := maps.Clone(r.actions), slices.Clone(r.rows)
	if err := fn(r); err != nil {
		r.actions, r.rows = actions, rows
		return err
	}

	return nil
}

func (*bookingRepo) LockPatient(context.Context, uuid.UUID) error { return nil }

func (r *bookingRepo) Slot(_ context.Context, id uuid.UUID, _ bool) (domain.Slot, error) {
	for _, slot := range r.slots {
		if slot.ID == id {
			return slot, nil
		}
	}

	return domain.Slot{}, domain.ErrNotFound
}

func (r *bookingRepo) SlotOccupied(_ context.Context, id, except uuid.UUID) (bool, error) {
	for _, row := range r.rows {
		if row.SlotID == id && row.ID != except && row.Status != domain.Cancelled {
			return true, nil
		}
	}

	return false, nil
}

func (r *bookingRepo) CountWeek(
	_ context.Context, patientID uuid.UUID, from, to time.Time, except uuid.UUID,
) (int64, error) {
	var count int64

	for _, row := range r.rows {
		if row.PatientID == patientID && row.ID != except && row.Status != domain.Cancelled &&
			!row.StartsAt.Before(from) && row.StartsAt.Before(to) {
			count++
		}
	}

	return count, nil
}

func (r *bookingRepo) Appointments(
	ctx context.Context, patientID uuid.UUID, filter application.AppointmentFilter,
) ([]domain.Appointment, error) {
	return (repository{rows: r.rows}).Appointments(ctx, patientID, filter)
}

func (r *bookingRepo) InsertAppointment(_ context.Context, row domain.Appointment) error {
	r.rows = append(r.rows, row)
	return nil
}

func (r *bookingRepo) InsertAction(_ context.Context, action domain.Action) error {
	r.actions[action.ID] = action
	return nil
}

func (r *bookingRepo) Action(_ context.Context, id, patientID uuid.UUID, _ bool) (domain.Action, error) {
	action, ok := r.actions[id]
	if !ok || action.PatientID != patientID {
		return domain.Action{}, domain.ErrNotFound
	}

	return action, nil
}

func (r *bookingRepo) SaveAction(_ context.Context, action domain.Action) error {
	r.actions[action.ID] = action
	return nil
}

func bookingFixture() (*application.Service, *bookingRepo) {
	core, catalog := searchFixture()
	repo := &bookingRepo{catalogRepo: catalog, actions: make(map[uuid.UUID]domain.Action)}
	core.Repo = repo

	return core, repo
}

func assertBookings(t *testing.T, core *application.Service, p domain.Principal, count int) []domain.Appointment {
	t.Helper()

	rows, err := core.ListAppointments(context.Background(), p, application.AppointmentFilter{})
	if err != nil || len(rows) != count {
		t.Fatalf("bookings=%+v err=%v; want %d bookings", rows, err, count)
	}

	return rows
}

func TestBookingRequiresLaterConfirmationAndReplaysTheSameAppointment(t *testing.T) {
	core, repo := bookingFixture()
	p := domain.Principal{PatientID: uuid.New()}
	step := 0
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		step++
		if step == 1 {
			return toolReply("search", "find_available_slots", `{"doctor":"Ana"}`), nil
		}

		if step != 2 {
			t.Fatal("confirmation must not be delegated to the model")
		}

		if !strings.Contains(in.Messages[0].Content, repo.slots[0].ID.String()) {
			t.Fatal("model needs the offered slots to resolve the patient's selection")
		}

		args := fmt.Sprintf(`{"slot_id":%q,"category":"consultation"}`, repo.slots[0].ID)

		return toolReply("prepare", "create_appointment", args), nil
	})
	a := agent.New(core, model)
	ctx := context.Background()

	first, err := a.ReplyConversation(ctx, p, uuid.Nil, "Procuro a Ana")
	if err != nil {
		t.Fatal(err)
	}

	proposal, err := a.ReplyConversation(ctx, p, first.ConversationID, "Quero o primeiro para consulta, confirmo")
	if err != nil || proposal.PendingAction == nil {
		t.Fatalf("missing proposal: %+v err=%v", proposal, err)
	}

	for _, text := range []string{"Ana Souza", "04/11/2026 às 14:00", "consulta", "Válida até", "confirma"} {
		if !strings.Contains(proposal.Message, text) {
			t.Fatalf("proposal missing %q: %s", text, proposal.Message)
		}
	}

	if proposal.PendingAction.ID == uuid.Nil ||
		proposal.PendingAction.ExpiresAt.Format(time.RFC3339) != "2026-10-31T22:10:00-03:00" {
		t.Fatalf("unexpected pending action: %+v", proposal.PendingAction)
	}

	assertBookings(t, core, p, 0)

	for _, message := range []string{"Sim, confirmo!", "Pode agendar", "sim"} {
		out, err := a.ReplyConversation(ctx, p, first.ConversationID, message)
		if err != nil || len(out.Appointments) != 1 {
			t.Fatalf("confirmation=%+v err=%v", out, err)
		}

		rows := assertBookings(t, core, p, 1)
		if rows[0].ID != out.Appointments[0].ID || rows[0].SlotID != repo.slots[0].ID ||
			rows[0].Category != "consultation" {
			t.Fatalf("confirmation did not reuse the proposed appointment: %+v", rows)
		}
	}
}

func preparedBooking(t *testing.T, beforeCompletion ...func()) (
	*agent.Agent, *application.Service, *bookingRepo, domain.Principal, agent.Result, *agent.Response,
) {
	t.Helper()

	core, repo := bookingFixture()
	p := domain.Principal{PatientID: uuid.New()}
	next := toolReply("search", "find_available_slots", `{"doctor":"Ana"}`)
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		for _, before := range beforeCompletion {
			before()
		}

		return next, nil
	})
	a := agent.New(core, model)

	first, err := a.ReplyConversation(context.Background(), p, uuid.Nil, "Ana")
	if err != nil {
		t.Fatal(err)
	}

	args := fmt.Sprintf(`{"slot_id":%q,"category":"consultation"}`, repo.slots[0].ID)
	next = toolReply("prepare", "create_appointment", args)

	proposal, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Primeiro, consulta")
	if err != nil || proposal.PendingAction == nil {
		t.Fatalf("proposal=%+v err=%v", proposal, err)
	}

	return a, core, repo, p, proposal, &next
}

func TestBookingRefusalAndAmbiguityCannotExecuteThePreviousProposal(t *testing.T) {
	for _, message := range []string{"não", "Não quero", "talvez", "sim, mas prefiro outro horário", "confirmo?"} {
		t.Run(message, func(t *testing.T) {
			a, core, _, p, proposal, next := preparedBooking(t)
			*next = toolReply("clarify", "create_appointment", `{}`)

			out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, message)
			if err != nil || out.PendingAction != nil {
				t.Fatalf("non-confirmation reused the proposal: %+v err=%v", out, err)
			}

			assertBookings(t, core, p, 0)

			out, err = a.ReplyConversation(context.Background(), p, proposal.ConversationID, "sim")
			if err != nil || len(out.Appointments) != 0 || !strings.Contains(out.Message, "Não há proposta") {
				t.Fatalf("old proposal was still confirmable: %+v err=%v", out, err)
			}
		})
	}
}

func TestBookingChangeRevokesConfirmationEvenWhenTheModelFails(t *testing.T) {
	a, core, _, p, proposal, next := preparedBooking(t)
	*next = toolReply("forged-confirmation", "confirm_action", `{}`)

	_, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Prefiro outro horário")
	if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
		t.Fatalf("model must not have a confirmation tool: %v", err)
	}

	out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "sim")
	if err != nil || len(out.Appointments) != 0 || !strings.Contains(out.Message, "Não há proposta") {
		t.Fatalf("failed change left stale confirmation authority: %+v err=%v", out, err)
	}

	assertBookings(t, core, p, 0)
}

func TestBookingCategoryChangePresentsANewProposalBeforeConfirming(t *testing.T) {
	a, core, repo, p, proposal, next := preparedBooking(t)
	*next = toolReply("change-category", "create_appointment", `{"category":"exam"}`)

	changed, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Prefiro exame")
	if err != nil || changed.PendingAction == nil || changed.PendingAction.ID == proposal.PendingAction.ID ||
		changed.PendingAction.Category != "exam" || !strings.Contains(changed.Message, "exame") {
		t.Fatalf("category change needs a fresh summary: %+v err=%v", changed, err)
	}

	assertBookings(t, core, p, 0)

	out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "confirmo")
	if err != nil || len(out.Appointments) != 1 || out.Appointments[0].Category != "exam" {
		t.Fatalf("changed proposal confirmation=%+v err=%v", out, err)
	}

	rows := assertBookings(t, core, p, 1)
	if rows[0].SlotID != repo.slots[0].ID {
		t.Fatalf("category change lost selected slot: %+v", rows)
	}
}

func TestBookingNewSearchDiscardsThePreviousSelection(t *testing.T) {
	a, core, _, p, proposal, next := preparedBooking(t)
	*next = toolReply("search", "find_available_slots", `{"doctor":"Desconhecido"}`)

	_, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Prefiro outro médico")
	if err != nil {
		t.Fatal(err)
	}

	*next = toolReply("category", "create_appointment", `{"category":"exam"}`)

	out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Quero exame")
	if err != nil || out.PendingAction != nil {
		t.Fatalf("new search reused previous offered slots: %+v err=%v", out, err)
	}

	_, err = a.ReplyConversation(context.Background(), p, proposal.ConversationID, "sim")
	if err != nil {
		t.Fatal(err)
	}

	assertBookings(t, core, p, 0)
}

func TestBookingExpiredProposalCannotReserveASlot(t *testing.T) {
	a, core, _, p, proposal, _ := preparedBooking(t)
	core.Clock = searchClock{proposal.PendingAction.ExpiresAt}

	for range 2 {
		_, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "sim")
		if domain.ErrorCode(err) != "ACTION_EXPIRED" {
			t.Fatalf("expired confirmation must use the existing action lifecycle: %v", err)
		}

		assertBookings(t, core, p, 0)
	}
}

func TestBookingConversationAndPatientIsolation(t *testing.T) {
	a, core, _, p, proposal, next := preparedBooking(t)
	other := domain.Principal{PatientID: uuid.New()}
	ctx := context.Background()

	_, err := a.ReplyConversation(ctx, other, proposal.ConversationID, "sim")
	if domain.ErrorCode(err) != "NOT_FOUND" {
		t.Fatalf("foreign conversation must be hidden: %v", err)
	}

	for _, patient := range []domain.Principal{p, other} {
		out, err := a.ReplyConversation(ctx, patient, uuid.Nil, "confirmo")
		if err != nil || len(out.Appointments) != 0 || !strings.Contains(out.Message, "Não há proposta") {
			t.Fatalf("confirmation crossed conversations: %+v err=%v", out, err)
		}

		args := fmt.Sprintf(`{"action_id":%q}`, proposal.PendingAction.ID)
		*next = toolReply("foreign-action", "create_appointment", args)

		_, err = a.ReplyConversation(ctx, patient, out.ConversationID, "Confirme a outra ação")
		if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
			t.Fatalf("model supplied an action ID: %v", err)
		}

		assertBookings(t, core, patient, 0)
	}

	// A foreign attempt must not consume the owner's proposal.
	out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "sim")
	if err != nil || len(out.Appointments) != 1 {
		t.Fatalf("owner lost proposal: %+v err=%v", out, err)
	}
}

func TestBookingSlotChangeRequiresANewSummary(t *testing.T) {
	core, repo := bookingFixture()
	second := repo.slots[0]
	second.ID, second.StartsAt = uuid.New(), second.StartsAt.Add(time.Hour)
	repo.slots = append(repo.slots, second)
	p := domain.Principal{PatientID: uuid.New()}
	next := toolReply("search", "find_available_slots", `{"doctor":"Ana"}`)
	a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) { return next, nil }))
	ctx := context.Background()

	first, err := a.ReplyConversation(ctx, p, uuid.Nil, "Ana")
	if err != nil {
		t.Fatal(err)
	}

	var previousID uuid.UUID

	for i, slot := range repo.slots {
		args := fmt.Sprintf(`{"slot_id":%q,"category":"procedure"}`, slot.ID)
		next = toolReply("choose", "create_appointment", args)

		out, err := a.ReplyConversation(ctx, p, first.ConversationID, "Prefiro esse horário para procedimento")
		if err != nil || out.PendingAction == nil || out.PendingAction.ID == previousID ||
			!strings.Contains(out.Message, slot.StartsAt.Format("15:04")) {
			t.Fatalf("slot %d did not receive a new summary: %+v err=%v", i, out, err)
		}

		previousID = out.PendingAction.ID

		assertBookings(t, core, p, 0)
	}

	out, err := a.ReplyConversation(ctx, p, first.ConversationID, "sim")
	if err != nil || len(out.Appointments) != 1 {
		t.Fatalf("confirmation=%+v err=%v", out, err)
	}

	rows := assertBookings(t, core, p, 1)
	if rows[0].SlotID != second.ID {
		t.Fatalf("confirmed the old slot: %+v", rows)
	}
}

func TestBookingClarifiesMissingChoicesBeforePreparation(t *testing.T) {
	for _, categoryFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("category first=%t", categoryFirst), func(t *testing.T) {
			core, repo := bookingFixture()
			p := domain.Principal{PatientID: uuid.New()}
			next := toolReply("search", "find_available_slots", `{"doctor":"Ana"}`)
			a := agent.New(
				core,
				modelFunc(func(context.Context, agent.Request) (agent.Response, error) { return next, nil }),
			)
			ctx := context.Background()

			first, err := a.ReplyConversation(ctx, p, uuid.Nil, "Ana")
			if err != nil {
				t.Fatal(err)
			}

			slotArgs := fmt.Sprintf(`{"slot_id":%q}`, repo.slots[0].ID)

			firstArgs, secondArgs := slotArgs, `{"category":"exam"}`
			if categoryFirst {
				firstArgs, secondArgs = secondArgs, firstArgs
			}

			next = toolReply("partial", "create_appointment", firstArgs)

			out, err := a.ReplyConversation(ctx, p, first.ConversationID, "Minha escolha parcial")
			if err != nil || out.PendingAction != nil || !strings.Contains(out.Message, "Qual") {
				t.Fatalf("missing choice did not prompt clarification: %+v err=%v", out, err)
			}

			assertBookings(t, core, p, 0)

			next = toolReply("complete", "create_appointment", secondArgs)

			out, err = a.ReplyConversation(ctx, p, first.ConversationID, "Minha outra escolha")
			if err != nil || out.PendingAction == nil || out.PendingAction.Category != "exam" {
				t.Fatalf("clarification lost the earlier choice: %+v err=%v", out, err)
			}
		})
	}
}

func TestBookingRejectsSlotsNotOfferedInTheConversation(t *testing.T) {
	a, core, _, p, proposal, next := preparedBooking(t)
	args := fmt.Sprintf(`{"slot_id":%q,"category":"consultation"}`, uuid.New())
	*next = toolReply("unoffered", "create_appointment", args)

	out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Quero outro")
	if err != nil || out.PendingAction != nil || !strings.Contains(out.Message, "horários apresentados") {
		t.Fatalf("accepted unoffered slot: %+v err=%v", out, err)
	}

	_, err = a.ReplyConversation(context.Background(), p, proposal.ConversationID, "sim")
	if err != nil {
		t.Fatal(err)
	}

	assertBookings(t, core, p, 0)
}

func TestBookingChangeRevokesConfirmationEvenWhenTheRequestIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelDuringReply := false
	a, core, _, p, proposal, _ := preparedBooking(t, func() {
		if cancelDuringReply {
			cancel()
		}
	})
	cancelDuringReply = true

	_, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Prefiro outro horário")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}

	out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "sim")
	if err != nil || len(out.Appointments) != 0 || !strings.Contains(out.Message, "Não há proposta") {
		t.Fatalf("cancelled change restored old confirmation authority: %+v err=%v", out, err)
	}

	assertBookings(t, core, p, 0)
}

func TestBookingAmbiguousSlotChangeMustBeClarified(t *testing.T) {
	a, core, _, p, proposal, next := preparedBooking(t)
	*next = toolReply("ambiguous-slot", "create_appointment", `{"slot_id":"","category":"exam"}`)

	out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Outro horário, para exame")
	if err != nil || out.PendingAction != nil || !strings.Contains(out.Message, "Qual dos horários") {
		t.Fatalf("ambiguous replacement must not reuse selected slot: %+v err=%v", out, err)
	}

	_, err = a.ReplyConversation(context.Background(), p, proposal.ConversationID, "sim")
	if err != nil {
		t.Fatal(err)
	}

	assertBookings(t, core, p, 0)
}

func TestBookingConfirmationCannotQueueBehindAnUnseenProposal(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	hold := false
	a, core, _, p, proposal, next := preparedBooking(t, func() {
		if hold {
			close(entered)
			<-release
		}
	})
	*next = toolReply("change", "create_appointment", `{"category":"exam"}`)
	hold = true

	type reply struct {
		result agent.Result
		err    error
	}

	done := make(chan reply, 1)

	go func() {
		out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Prefiro exame")
		done <- reply{out, err}
	}()

	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, earlyErr := a.ReplyConversation(ctx, p, proposal.ConversationID, "sim")

	close(release)

	changed := <-done
	if changed.err != nil || changed.result.PendingAction == nil {
		t.Fatalf("replacement proposal=%+v err=%v", changed.result, changed.err)
	}

	if domain.ErrorCode(earlyErr) != "INVALID_INPUT" {
		t.Fatalf("confirmation submitted before summary must be rejected immediately: %v", earlyErr)
	}

	assertBookings(t, core, p, 0)

	out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "sim")
	if err != nil || len(out.Appointments) != 1 || out.Appointments[0].Category != "exam" {
		t.Fatalf("later confirmation=%+v err=%v", out, err)
	}
}
