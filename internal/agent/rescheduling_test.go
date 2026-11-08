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

type reschedulingRepo struct {
	*bookingRepo
	updateErr error
}

func (r *reschedulingRepo) Appointment(
	_ context.Context, id, patientID uuid.UUID, _ bool,
) (domain.Appointment, error) {
	for _, row := range r.rows {
		if row.ID == id && row.PatientID == patientID {
			return row, nil
		}
	}

	return domain.Appointment{}, domain.ErrNotFound
}

func (r *reschedulingRepo) Appointments(
	ctx context.Context, patientID uuid.UUID, filter application.AppointmentFilter,
) ([]domain.Appointment, error) {
	rows, err := r.bookingRepo.Appointments(ctx, patientID, filter)
	if err != nil {
		return nil, err
	}

	filtered := []domain.Appointment{}

	for _, row := range rows {
		if !filter.From.IsZero() && row.StartsAt.Before(filter.From) {
			continue
		}

		if !filter.To.IsZero() && !row.StartsAt.Before(filter.To) {
			continue
		}

		filtered = append(filtered, row)
	}

	return filtered, nil
}

func (r *reschedulingRepo) Transaction(ctx context.Context, fn func(application.Repository) error) error {
	return r.bookingRepo.Transaction(ctx, func(application.Repository) error { return fn(r) })
}

func (r *reschedulingRepo) UpdateAppointment(_ context.Context, row domain.Appointment, version int) error {
	for i, previous := range r.rows {
		if previous.ID == row.ID && previous.Version == version {
			r.rows[i] = row
			return r.updateErr
		}
	}

	return domain.ErrAppointmentChanged
}

func (r *reschedulingRepo) Slots(ctx context.Context, filter application.SlotFilter) ([]domain.Slot, error) {
	rows, err := r.catalogRepo.Slots(ctx, filter)
	if err != nil {
		return nil, err
	}

	available := []domain.Slot{}

	for _, slot := range rows {
		occupied, err := r.SlotOccupied(ctx, slot.ID, uuid.Nil)
		if err != nil {
			return nil, err
		}

		if !occupied {
			available = append(available, slot)
		}
	}

	return available, nil
}

func reschedulingFixture() (*application.Service, *reschedulingRepo, domain.Principal, domain.Appointment) {
	core, booking := bookingFixture()
	repo := &reschedulingRepo{bookingRepo: booking}
	core.Repo = repo
	p := domain.Principal{PatientID: uuid.New()}
	slot := repo.slots[0]
	original := domain.Appointment{
		ID: uuid.New(), PatientID: p.PatientID, SlotID: slot.ID, DoctorID: slot.DoctorID,
		DoctorName: slot.DoctorName, StartsAt: slot.StartsAt, Category: "exam", Status: domain.Scheduled, Version: 1,
	}
	repo.rows = []domain.Appointment{original}
	destination := slot
	destination.ID, destination.StartsAt = uuid.New(), slot.StartsAt.AddDate(0, 0, 1)
	repo.slots = append(repo.slots, destination)

	return core, repo, p, original
}

func TestReschedulingClarifiesAmbiguousOwnAppointments(t *testing.T) {
	core, repo, p, original := reschedulingFixture()
	second := original
	second.ID, second.SlotID, second.StartsAt = uuid.New(), repo.slots[1].ID, repo.slots[1].StartsAt
	otherPatient := original
	otherPatient.ID, otherPatient.PatientID = uuid.New(), uuid.New()
	repo.rows = append(repo.rows, second, otherPatient)
	step := 0
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		step++
		args := `{}`

		if step == 2 {
			if !strings.Contains(in.Messages[0].Content, second.ID.String()) ||
				strings.Contains(in.Messages[0].Content, otherPatient.ID.String()) {
				t.Fatal("follow-up must expose only the patient's verified candidate IDs to the model")
			}

			args = fmt.Sprintf(`{"appointment_id":%q}`, second.ID)
		}

		return toolReply("identify", "select_appointment", args), nil
	})
	a := agent.New(core, model)
	ctx := context.Background()

	out, err := a.Reply(ctx, p, "Quero remarcar meu exame")
	if err != nil || len(out.Appointments) != 2 || out.PendingAction != nil ||
		!strings.Contains(out.Message, "Qual") {
		t.Fatalf("expected own candidates and clarification: %+v err=%v", out, err)
	}

	out, err = a.ReplyConversation(ctx, p, out.ConversationID, "O de quinta-feira")
	if err != nil || len(out.Appointments) != 1 || out.Appointments[0].ID != second.ID ||
		!strings.Contains(out.Message, "novo horário") {
		t.Fatalf("expected selected original: %+v err=%v", out, err)
	}

	rows := assertBookings(t, core, p, 2)
	if rows[0] != original || rows[1] != second {
		t.Fatalf("identification changed appointments: %+v", rows)
	}
}

func preparedRescheduling(t *testing.T) (
	*agent.Agent, *application.Service, *reschedulingRepo, domain.Principal,
	domain.Appointment, agent.Result, *agent.Response,
) {
	t.Helper()

	core, repo, p, original := reschedulingFixture()
	otherDoctor := repo.doctors[0]
	otherDoctor.ID = uuid.New()
	repo.doctors = append(repo.doctors, otherDoctor)
	otherSlot := repo.slots[1]
	otherSlot.ID, otherSlot.DoctorID = uuid.New(), otherDoctor.ID
	repo.slots = append(repo.slots, otherSlot)
	next := toolReply("identify", "select_appointment", `{}`)
	a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		return next, nil
	}))
	ctx := context.Background()

	out, err := a.Reply(ctx, p, "Quero remarcar meu exame")
	if err != nil {
		t.Fatal(err)
	}

	next = toolReply("search", "find_available_slots", `{}`)

	out, err = a.ReplyConversation(ctx, p, out.ConversationID, "Quais horários há?")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 ||
		out.Availability.Slots[0].ID != repo.slots[1].ID {
		t.Fatalf("destination search must keep the original doctor ID: %+v err=%v", out, err)
	}

	next = toolReply("prepare", "reschedule_appointment", fmt.Sprintf(`{"slot_id":%q}`, repo.slots[1].ID))

	out, err = a.ReplyConversation(ctx, p, out.ConversationID, "O primeiro, pode remarcar")
	if err != nil || out.PendingAction == nil {
		t.Fatalf("expected rescheduling proposal: %+v err=%v", out, err)
	}

	return a, core, repo, p, original, out, &next
}

func TestReschedulingRequiresLaterConfirmationAndPreservesIdentity(t *testing.T) {
	a, core, repo, p, original, proposal, next := preparedRescheduling(t)
	ctx := context.Background()

	for _, text := range []string{"04/11/2026 às 14:00", "05/11/2026 às 14:00", "Ana Souza", "exame", "confirma"} {
		if !strings.Contains(proposal.Message, text) {
			t.Fatalf("proposal missing %q: %s", text, proposal.Message)
		}
	}

	if rows := assertBookings(t, core, p, 1); rows[0] != original {
		t.Fatalf("preparation changed original: %+v", rows)
	}

	action, err := core.GetAction(ctx, p, proposal.PendingAction.ID)
	if err != nil || action.Operation != domain.RescheduleAppointment || action.AppointmentID != original.ID {
		t.Fatalf("proposal must bind the core rescheduling action: %+v err=%v", action, err)
	}

	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	for range 2 {
		out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Sim, confirmo a remarcação")
		if err != nil || out.PendingAction != nil || len(out.Appointments) != 1 ||
			!strings.Contains(out.Message, "Remarcação confirmada") {
			t.Fatalf("confirmation=%+v err=%v", out, err)
		}

		rows := assertBookings(t, core, p, 1)
		row := rows[0]
		if row.ID != original.ID || row.DoctorID != original.DoctorID || row.Category != original.Category ||
			row.SlotID != repo.slots[1].ID || row.Version != 2 || out.Appointments[0].ID != original.ID {
			t.Fatalf("rescheduling changed identity or replayed the mutation: %+v", row)
		}
	}

	available, err := core.FindSlots(ctx, application.SearchInput{DoctorID: original.DoctorID})
	if err != nil || len(available.Slots) != 1 || available.Slots[0].ID != original.SlotID {
		t.Fatalf("rescheduling must release the old slot: %+v err=%v", available, err)
	}
}

func TestReschedulingRefusalAndQuestionsPreserveOriginalAndRevokeProposal(t *testing.T) {
	for _, decision := range []string{"reject", "clarify"} {
		t.Run(decision, func(t *testing.T) {
			a, core, _, p, original, proposal, next := preparedRescheduling(t)
			*next = toolReply("decision", "respond_to_proposal", fmt.Sprintf(`{"decision":%q}`, decision))
			ctx := context.Background()

			out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Prefiro deixar como está")
			if err != nil || out.PendingAction != nil || !strings.Contains(out.Message, "original") {
				t.Fatalf("non-confirmation must explain preservation: %+v err=%v", out, err)
			}

			*next = toolReply("stale", "respond_to_proposal", `{"decision":"confirm"}`)

			_, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo")
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("discarded proposal accepted confirmation: %v", err)
			}

			if rows := assertBookings(t, core, p, 1); rows[0] != original {
				t.Fatalf("refusal changed original: %+v", rows)
			}
		})
	}
}

func TestReschedulingDoctorChangeStartsNewBookingWithoutChangingOriginal(t *testing.T) {
	a, core, repo, p, original, proposal, next := preparedRescheduling(t)
	other := domain.Doctor{ID: uuid.New(), SpecialtyID: repo.doctors[0].SpecialtyID, Name: "Dr. Bruno Lima"}
	repo.doctors = append(repo.doctors, other)
	slot := repo.slots[1]
	slot.ID, slot.DoctorID, slot.DoctorName = uuid.New(), other.ID, other.Name
	repo.slots = append(repo.slots, slot)
	*next = toolReply("change", "find_available_slots", `{"doctor":"Bruno","specialty":""}`)
	ctx := context.Background()

	out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Sim, mas quero com o Bruno")
	if err != nil || out.PendingAction != nil || !strings.Contains(out.Message, "novo agendamento") ||
		!strings.Contains(out.Message, "original") || out.Availability == nil ||
		len(out.Availability.Slots) != 1 || out.Availability.Slots[0].ID != slot.ID {
		t.Fatalf("doctor change must explain new booking intent: %+v err=%v", out, err)
	}

	*next = toolReply("search", "find_available_slots", `{}`)

	out, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Mostre os horários dele")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 ||
		out.Availability.Slots[0].ID != slot.ID {
		t.Fatalf("new search did not use Bruno: %+v err=%v", out, err)
	}

	*next = toolReply("old-intent", "reschedule_appointment", fmt.Sprintf(`{"slot_id":%q}`, slot.ID))

	out, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Remarque para esse")
	if err != nil || out.PendingAction != nil {
		t.Fatalf("changed doctor reused rescheduling authority: %+v err=%v", out, err)
	}

	if rows := assertBookings(t, core, p, 1); rows[0] != original {
		t.Fatalf("doctor change changed original: %+v", rows)
	}
}

func TestReschedulingCategoryChangeRequiresSeparateNewBookingConfirmation(t *testing.T) {
	a, core, repo, p, original, proposal, next := preparedRescheduling(t)
	*next = toolReply("change", "create_appointment", fmt.Sprintf(
		`{"slot_id":%q,"category":"consultation"}`, repo.slots[1].ID,
	))
	ctx := context.Background()

	out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Sim, mas prefiro consulta")
	if err != nil || out.PendingAction == nil || !strings.Contains(out.Message, "novo agendamento") ||
		!strings.Contains(out.Message, "original") {
		t.Fatalf("category change must explain a separate proposal: %+v err=%v", out, err)
	}

	action, err := core.GetAction(ctx, p, out.PendingAction.ID)
	if err != nil || action.Operation != domain.CreateAppointment || action.Category != "consultation" {
		t.Fatalf("category change reused a rescheduling action: %+v err=%v", action, err)
	}

	if rows := assertBookings(t, core, p, 1); rows[0] != original {
		t.Fatalf("new proposal changed original: %+v", rows)
	}

	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	out, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo o novo agendamento")
	if err != nil || len(out.Appointments) != 1 || out.Appointments[0].ID == original.ID {
		t.Fatalf("new booking confirmation=%+v err=%v", out, err)
	}

	if rows := assertBookings(t, core, p, 2); rows[0] != original || rows[1].Category != "consultation" {
		t.Fatalf("new booking must preserve original: %+v", rows)
	}
}

func TestReschedulingFailuresPreserveOriginalReservation(t *testing.T) {
	for _, scenario := range []string{"destination conflict", "write failure", "expired", "original changed"} {
		t.Run(scenario, func(t *testing.T) {
			a, core, repo, p, original, proposal, next := preparedRescheduling(t)
			wantCode := "SLOT_UNAVAILABLE"

			switch scenario {
			case "destination conflict":
				competitor := original
				competitor.ID, competitor.PatientID, competitor.SlotID = uuid.New(), uuid.New(), repo.slots[1].ID
				competitor.StartsAt = repo.slots[1].StartsAt
				repo.rows = append(repo.rows, competitor)
			case "write failure":
				repo.updateErr = errors.New("database write failed after updating the row")
				wantCode = "INTERNAL_ERROR"
			case "expired":
				core.Clock = searchClock{proposal.PendingAction.ExpiresAt}
				wantCode = "ACTION_EXPIRED"
			case "original changed":
				repo.rows[0].Version++
				original.Version++
				wantCode = "APPOINTMENT_CHANGED"
			}

			*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)
			ctx := context.Background()

			out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo a remarcação")
			if domain.ErrorCode(err) != wantCode || out.Message != "" || len(out.Appointments) != 0 {
				t.Fatalf("failure reported success: %+v err=%v; want %s", out, err, wantCode)
			}

			if rows := assertBookings(t, core, p, 1); rows[0] != original {
				t.Fatalf("failed operation lost original reservation: %+v", rows)
			}

			available, err := core.FindSlots(ctx, application.SearchInput{DoctorID: original.DoctorID})
			if err != nil {
				t.Fatal(err)
			}

			for _, slot := range available.Slots {
				if slot.ID == original.SlotID {
					t.Fatal("failed operation released the original slot")
				}
			}

			_, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo de novo")

			retryCode := "MODEL_INVALID_RESPONSE"
			if scenario == "write failure" {
				retryCode = "ACTION_NOT_COMPLETED"
			}

			if domain.ErrorCode(err) != retryCode {
				t.Fatalf("failed confirmation retained proposal authority: %v", err)
			}
		})
	}
}

func TestReschedulingIdentificationFiltersFutureOwnAppointments(t *testing.T) {
	for _, scenario := range []string{
		"matching", "foreign", "cancelled", "past", "different category", "outside dates",
	} {
		t.Run(scenario, func(t *testing.T) {
			core, repo, p, original := reschedulingFixture()
			args := `{"doctor":"Ana","category":"exam"}`

			if scenario == "foreign" {
				repo.rows[0].PatientID = uuid.New()
				args = fmt.Sprintf(`{"appointment_id":%q}`, original.ID)
			}

			if scenario == "cancelled" {
				repo.rows[0].Status = domain.Cancelled
			}

			if scenario == "past" {
				repo.rows[0].StartsAt = core.Clock.Now()
			}

			if scenario == "different category" {
				args = `{"category":"consultation"}`
			}

			if scenario == "outside dates" {
				args = `{"from":"2026-11-05T00:00:00-03:00","to":"2026-11-06T00:00:00-03:00"}`
			}

			a := agent.New(core, modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
				return toolReply("identify", "select_appointment", args), nil
			}))

			out, err := a.Reply(context.Background(), p, "Remarcar meu exame com a Ana")

			want := 0
			if scenario == "matching" {
				want = 1
			}

			if err != nil || len(out.Appointments) != want || out.PendingAction != nil {
				t.Fatalf("identification=%+v err=%v; want %d appointments", out, err, want)
			}
		})
	}
}

func TestReschedulingRejectsUnpresentedDestinationsAndChangedInvariants(t *testing.T) {
	for _, scenario := range []string{
		"unknown slot", "original slot", "ambiguous slot", "doctor", "category", "action",
	} {
		t.Run(scenario, func(t *testing.T) {
			a, core, repo, p, original, proposal, next := preparedRescheduling(t)
			args := fmt.Sprintf(`{"slot_id":%q}`, uuid.New())
			invalid := false

			switch scenario {
			case "original slot":
				args = fmt.Sprintf(`{"slot_id":%q}`, original.SlotID)
			case "ambiguous slot":
				args = `{"slot_id":""}`
			case "doctor", "category", "action":
				args = fmt.Sprintf(`{"slot_id":%q,%q:"changed"}`, repo.slots[1].ID, scenario)
				invalid = true
			}

			*next = toolReply("invalid-target", "reschedule_appointment", args)

			out, err := a.ReplyConversation(context.Background(), p, proposal.ConversationID, "Mude a proposta")
			if (invalid && domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE") ||
				(!invalid && err != nil) || out.PendingAction != nil {
				t.Fatalf("unsafe target accepted: %+v err=%v", out, err)
			}

			if rows := assertBookings(t, core, p, 1); rows[0] != original {
				t.Fatalf("unsafe preparation changed original: %+v", rows)
			}
		})
	}
}

func TestReschedulingCanReturnToTheOriginalSlotAfterConfirmation(t *testing.T) {
	a, core, _, p, original, proposal, next := preparedRescheduling(t)
	*next = toolReply("confirm", "respond_to_proposal", `{"decision":"confirm"}`)

	ctx := context.Background()
	if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo"); err != nil {
		t.Fatal(err)
	}

	*next = toolReply("search-again", "find_available_slots", `{}`)

	out, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Quero voltar ao horário anterior")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 ||
		out.Availability.Slots[0].ID != original.SlotID {
		t.Fatalf("search used the outdated original after confirmation: %+v err=%v", out, err)
	}

	*next = toolReply("prepare-again", "reschedule_appointment", fmt.Sprintf(`{"slot_id":%q}`, original.SlotID))

	out, err = a.ReplyConversation(ctx, p, proposal.ConversationID, "O de quarta")
	if err != nil || out.PendingAction == nil || !strings.Contains(out.Message, "de 05/11/2026") {
		t.Fatalf("new proposal must describe the current reservation: %+v err=%v", out, err)
	}

	*next = toolReply("confirm-again", "respond_to_proposal", `{"decision":"confirm"}`)

	if _, err := a.ReplyConversation(ctx, p, proposal.ConversationID, "Confirmo a volta"); err != nil {
		t.Fatal(err)
	}

	rows := assertBookings(t, core, p, 1)
	if rows[0].SlotID != original.SlotID || rows[0].ID != original.ID || rows[0].Version != 3 {
		t.Fatalf("second rescheduling=%+v", rows)
	}
}
