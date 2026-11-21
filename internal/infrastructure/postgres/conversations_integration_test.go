//go:build integration

package postgres

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
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func TestConversationSurvivesStoreRestart(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	id := uuid.New()
	ctx := context.Background()
	locked, err := f.store.Acquire(ctx, p.PatientID, id, agent.ConversationAccess{CreateConversation: true})
	f.must(err)

	state := agent.Conversation{
		Intent: "reschedule_appointment", Revision: 7,
		History:      []agent.Message{{Role: "user", Content: "Prefiro à tarde"}},
		Selection:    agent.BookingSelection{SlotID: uuid.New(), Category: "exam"},
		Proposal:     &agent.PendingAction{ID: uuid.New(), Summary: "Resumo", ExpiresAt: f.clock.now},
		Completed:    &agent.CompletedAction{ID: uuid.New()},
		Uncertain:    &agent.UncertainConfirmation{ID: uuid.New()},
		Offered:      []agent.Slot{{ID: uuid.New(), DoctorName: "Ana", StartsAt: f.clock.now}},
		Candidates:   []agent.Appointment{{ID: uuid.New(), Category: "exam"}},
		Rescheduling: &contract.Rescheduling{DoctorID: f.doctor, SlotID: uuid.New()},
	}
	f.must(locked.Save(ctx, state))
	locked.Release()
	f.must(f.store.Close())

	restarted, err := Open(f.dsn)
	f.must(err)
	t.Cleanup(func() { f.must(restarted.Close()) })

	resumed, err := restarted.Acquire(ctx, p.PatientID, id, agent.ConversationAccess{})
	f.must(err)

	defer resumed.Release()

	want, err := json.Marshal(state)
	f.must(err)

	got := resumed.Snapshot()
	encoded, err := json.Marshal(got)
	f.must(err)

	if string(encoded) != string(want) || got.Rescheduling.DoctorID != f.doctor ||
		got.Rescheduling.SlotID != state.Rescheduling.SlotID {
		t.Fatalf("conversation changed after restart: %s, want %s", encoded, want)
	}

	got.Proposal.Summary = "local change"
	if resumed.Snapshot().Proposal.Summary != "Resumo" {
		t.Fatal("snapshot changes were implicitly saved")
	}
}

func TestConversationKeepsMessagesOutsideModelWindow(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	a, store := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool("find_available_slots", `{}`), nil
	}))

	ctx := context.Background()
	id := uuid.Nil
	for turn := range 12 {
		out, err := a.ReplyConversation(ctx, p, id, fmt.Sprintf("Mensagem %d", turn))
		f.must(err)

		id = out.ConversationID
	}

	f.must(store.Close())

	resumed, restarted := f.assistant(
		conversationModel(func(_ context.Context, in agent.Request) (agent.Response, error) {
			if len(in.Messages) != 22 || in.Messages[1].Content != "Mensagem 2" {
				t.Fatalf("model context must keep only the last ten exchanges: %+v", in.Messages)
			}

			return conversationTool("find_available_slots", `{}`), nil
		}),
	)
	locked, err := restarted.Acquire(ctx, p.PatientID, id, agent.ConversationAccess{})
	f.must(err)

	state := locked.Snapshot()
	locked.Release()

	if len(state.History) != 24 || state.History[0].Content != "Mensagem 0" {
		t.Fatalf("older messages were lost: %+v", state.History)
	}

	_, err = resumed.ReplyConversation(ctx, p, id, "Continue")
	f.must(err)
}

func conversationTool(name, args string) agent.Response {
	return agent.Response{ToolCalls: []agent.ToolCall{{ID: "call", Name: name, Arguments: json.RawMessage(args)}}}
}

func (f *fixture) assistant(model agent.Model) (*agent.Agent, *Store) {
	f.t.Helper()
	store, err := Open(f.dsn)
	f.must(err)
	f.t.Cleanup(func() { f.must(store.Close()) })
	core := &application.Service{Repo: store, Clock: f.clock, Location: f.service.Location}

	return agent.NewWithConversations(core, model, store), store
}

func TestSearchPreferencesAndHistorySurviveRestart(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	tuesday := f.slot(8, 14)
	wednesday := f.slot(9, 14)
	f.slot(9, 9)

	ctx := context.Background()
	first, store := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool("find_available_slots",
			`{"specialty":"Cardiologia","date":"next_week","period":"afternoon"}`), nil
	}))
	out, err := first.Reply(ctx, p, "Cardiologia semana que vem à tarde")
	f.must(err)

	if out.Availability == nil || len(out.Availability.Slots) != 2 {
		t.Fatalf("expected two afternoon slots: %+v", out)
	}

	f.must(store.Close())

	date := "after_tuesday"
	resumed, _ := f.assistant(conversationModel(func(_ context.Context, in agent.Request) (agent.Response, error) {
		if len(in.Messages) < 4 || in.Messages[1].Content != "Cardiologia semana que vem à tarde" ||
			!strings.Contains(in.Messages[0].Content, "Intenção anterior: find_available_slots") {
			t.Fatalf("conversation context was lost: %+v", in.Messages)
		}

		return conversationTool("find_available_slots", `{"date":"`+date+`"}`), nil
	}))
	out, err = resumed.ReplyConversation(ctx, p, out.ConversationID, "Depois de terça")
	f.must(err)

	if out.Availability == nil || len(out.Availability.Slots) != 1 || out.Availability.Slots[0].ID != wednesday {
		t.Fatalf("lost search preferences after restart: %+v", out)
	}

	date = "after_monday"
	out, err = resumed.ReplyConversation(ctx, p, out.ConversationID, "Na verdade, depois de segunda")
	f.must(err)

	if out.Availability == nil || len(out.Availability.Slots) != 2 || out.Availability.Slots[0].ID != tuesday {
		t.Fatalf("lost original selected interval: %+v", out)
	}
}

// Every turn uses a newly opened store and service, with no in-process conversation state.
func (f *fixture) turn(
	p domain.Principal, id uuid.UUID, tool, args, message string,
) (agent.Result, error) {
	f.t.Helper()
	a, store := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool(tool, args), nil
	}))
	defer func() { f.must(store.Close()) }()

	return a.ReplyConversation(context.Background(), p, id, message)
}

func (f *fixture) conversationProposal(p domain.Principal, slot uuid.UUID) agent.Result {
	f.t.Helper()
	out, err := f.turn(p, uuid.Nil, "find_available_slots", `{"doctor":"Ana"}`, "Horários com Ana")
	f.must(err)

	id := out.ConversationID
	out, err = f.turn(p, id, "create_appointment", fmt.Sprintf(`{"slot_id":%q}`, slot), "Esse horário")
	f.must(err)

	if out.PendingAction != nil {
		f.t.Fatal("incomplete selection prepared a proposal")
	}

	out, err = f.turn(p, id, "create_appointment", `{"category":"consultation"}`, "Consulta")
	f.must(err)

	if out.PendingAction == nil {
		f.t.Fatalf("selection did not survive restart: %+v", out)
	}

	return out
}

func TestConfirmationAfterRestartUsesOnlyThePresentedAction(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	slot := f.slot(1, 14)
	proposal := f.conversationProposal(p, slot)
	unrelated := f.prepareCreate(p, f.slot(2, 14))

	out, err := f.turn(p, proposal.ConversationID, "respond_to_proposal", `{"decision":"confirm"}`, "Sim")
	f.must(err)

	if len(out.Appointments) != 1 {
		t.Fatalf("missing confirmed booking: %+v", out)
	}

	appointmentID := out.Appointments[0].ID
	action, err := f.service.GetAction(context.Background(), p, unrelated.ID)
	f.must(err)

	if action.Status != domain.Pending {
		t.Fatalf("confirmed an unrelated action: %+v", action)
	}

	f.clock.now = proposal.PendingAction.ExpiresAt.AddDate(0, 0, 1)
	out, err = f.turn(p, proposal.ConversationID, "respond_to_proposal", `{"decision":"confirm"}`, "Sim novamente")
	f.must(err)

	if len(out.Appointments) != 1 || out.Appointments[0].ID != appointmentID {
		t.Fatalf("restart lost idempotency reference: %+v", out)
	}

	rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
	f.must(err)

	if len(rows) != 1 || rows[0].SlotID != slot {
		t.Fatalf("confirmation duplicated or chose another booking: %+v", rows)
	}
}

func TestExpiredOrUnavailableProposalAfterRestartRequiresFreshPreparation(t *testing.T) {
	for _, scenario := range []string{"expired", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			p, _ := f.patient()
			slot := f.slot(1, 14)
			proposal := f.conversationProposal(p, slot)
			want := "ACTION_EXPIRED"

			if scenario == "expired" {
				f.clock.now = proposal.PendingAction.ExpiresAt
			} else {
				other, _ := f.patient()
				f.book(other, slot)

				want = "SLOT_UNAVAILABLE"
			}

			out, err := f.turn(p, proposal.ConversationID, "respond_to_proposal", `{"decision":"confirm"}`, "Sim")
			code(t, err, want)

			if out.Message != "" || len(out.Appointments) != 0 {
				t.Fatalf("obsolete proposal returned success: %+v", out)
			}

			_, err = f.turn(p, proposal.ConversationID, "respond_to_proposal", `{"decision":"confirm"}`, "Sim")
			code(t, err, "MODEL_INVALID_RESPONSE")
			rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
			f.must(err)

			if len(rows) != 0 {
				t.Fatalf("obsolete proposal booked: %+v", rows)
			}
		})
	}
}

func TestConversationAfterRestartRejectsOtherPatients(t *testing.T) {
	f := setup(t)
	owner, _ := f.patient()
	other, _ := f.patient()
	proposal := f.conversationProposal(owner, f.slot(1, 14))
	_, err := f.turn(other, proposal.ConversationID, "respond_to_proposal", `{"decision":"confirm"}`, "Sim")
	code(t, err, "NOT_FOUND")
	_, err = f.store.Acquire(context.Background(), other.PatientID, proposal.ConversationID,
		agent.ConversationAccess{CreateConversation: true})
	code(t, err, "NOT_FOUND")

	otherConversation, err := f.turn(other, uuid.Nil, "find_available_slots", `{}`, "Outra conversa")
	f.must(err)
	_, err = f.turn(other, otherConversation.ConversationID, "respond_to_proposal", `{"decision":"confirm"}`, "Sim")
	code(t, err, "MODEL_INVALID_RESPONSE")

	out, err := f.turn(owner, proposal.ConversationID, "respond_to_proposal", `{"decision":"confirm"}`, "Sim")
	f.must(err)

	if len(out.Appointments) != 1 {
		t.Fatalf("foreign access changed the owner's proposal: %+v", out)
	}
}

func TestConversationAccessAcrossInstances(t *testing.T) {
	f := setup(t)
	owner, _ := f.patient()
	other, _ := f.patient()
	id := uuid.New()
	ctx := context.Background()
	locked, err := f.store.Acquire(ctx, owner.PatientID, id, agent.ConversationAccess{CreateConversation: true})
	f.must(err)

	defer locked.Release()

	f.must(locked.Save(ctx, agent.Conversation{Intent: "saved"}))
	_, second := f.assistant(nil)

	_, err = second.Acquire(ctx, owner.PatientID, id, agent.ConversationAccess{})
	if !errors.Is(err, agent.ErrConversationBusy) {
		t.Fatalf("concurrent turn did not fail immediately: %v", err)
	}

	_, err = second.Acquire(ctx, other.PatientID, id, agent.ConversationAccess{})
	code(t, err, "NOT_FOUND")

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()

	_, err = second.Acquire(waitCtx, owner.PatientID, id, agent.ConversationAccess{Wait: true})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting ignored cancellation: %v", err)
	}

	cancelled, stop := context.WithCancel(ctx)
	stop()

	err = locked.Save(cancelled, agent.Conversation{Intent: "unsaved"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("save ignored cancellation: %v", err)
	}

	locked.Release()

	resumed, err := second.Acquire(ctx, owner.PatientID, id, agent.ConversationAccess{Wait: true})
	f.must(err)

	defer resumed.Release()

	if resumed.Snapshot().Intent != "saved" {
		t.Fatal("failed save replaced the durable checkpoint")
	}

	err = locked.Save(ctx, agent.Conversation{Intent: "after release"})
	if err == nil {
		t.Fatal("released access permitted a write")
	}
}

func TestUnsavedConversationCannotBeReopened(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	id := uuid.New()
	ctx := context.Background()
	locked, err := f.store.Acquire(ctx, p.PatientID, id, agent.ConversationAccess{CreateConversation: true})
	f.must(err)
	locked.Release()

	_, err = f.store.Acquire(ctx, p.PatientID, id, agent.ConversationAccess{})
	code(t, err, "NOT_FOUND")
}

func TestConversationWriteFailuresNeverReportSuccess(t *testing.T) {
	for _, checkpoint := range []string{"Proposal", "Uncertain", "Completed"} {
		t.Run(checkpoint, func(t *testing.T) {
			f := setup(t)
			p, _ := f.patient()

			slot := f.slot(1, 14)

			var proposal agent.Result
			if checkpoint != "Proposal" {
				proposal = f.conversationProposal(p, slot)
			} else {
				var err error

				proposal, err = f.turn(p, uuid.Nil, "find_available_slots", `{"doctor":"Ana"}`, "Horários")
				f.must(err)
			}

			// Inject a real database boundary failure at one durable checkpoint.
			f.must(f.store.db.Exec(`CREATE FUNCTION reject_conversation_write() RETURNS trigger
				LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected persistence failure'; END $$`).Error)
			f.must(f.store.db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_conversation BEFORE UPDATE ON conversations
				FOR EACH ROW WHEN (NEW.state -> 'State' -> '%s' <> 'null'::jsonb)
				EXECUTE FUNCTION reject_conversation_write()`, checkpoint)).Error)

			tool, args := "respond_to_proposal", `{"decision":"confirm"}`
			if checkpoint == "Proposal" {
				tool = "create_appointment"
				args = fmt.Sprintf(`{"slot_id":%q,"category":"consultation"}`, slot)
			}

			out, err := f.turn(p, proposal.ConversationID, tool, args, "Sim")
			if err == nil || out.Message != "" || out.PendingAction != nil || len(out.Appointments) != 0 {
				t.Fatalf("failed persistence reported success: %+v err=%v", out, err)
			}

			f.must(f.store.db.Exec("DROP TRIGGER fail_conversation ON conversations").Error)

			// A fresh process must query a committed result, never execute a new mutation.
			out, err = f.turn(p, proposal.ConversationID, "respond_to_proposal", `{"decision":"confirm"}`, "Sim")
			wantBookings := 0

			if checkpoint == "Completed" {
				f.must(err)

				wantBookings = 1

				if len(out.Appointments) != 1 || !strings.Contains(out.Message, "confirmação anterior") {
					t.Fatalf("committed confirmation was not reconciled: %+v", out)
				}
			} else {
				code(t, err, "MODEL_INVALID_RESPONSE")
			}

			rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
			f.must(err)

			if len(rows) != wantBookings {
				t.Fatalf("unexpected mutations after failed %s save: %+v", checkpoint, rows)
			}
		})
	}
}

func TestReschedulingSelectionAndVersionSurviveRestart(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("original_changed=%t", changed), func(t *testing.T) {
			f := setup(t)
			p, _ := f.patient()
			original := f.book(p, f.slot(1, 14))
			destination := f.slot(2, 15)
			out, err := f.turn(p, uuid.Nil, "select_appointment", `{}`, "Quero remarcar")
			f.must(err)

			id := out.ConversationID
			out, err = f.turn(p, id, "find_available_slots", `{}`, "Mostre os horários")
			f.must(err)

			if out.Availability == nil || len(out.Availability.Slots) != 1 ||
				out.Availability.Slots[0].ID != destination {
				t.Fatalf("lost the original doctor after restart: %+v", out)
			}

			out, err = f.turn(p, id, "reschedule_appointment",
				fmt.Sprintf(`{"slot_id":%q}`, destination), "Esse horário")
			f.must(err)

			if out.PendingAction == nil {
				t.Fatalf("lost rescheduling selection: %+v", out)
			}

			if changed {
				otherAction := f.prepare(p, application.PrepareInput{
					Operation: domain.CancelAppointment, AppointmentID: original.ID,
				})
				_, err := f.service.Confirm(context.Background(), p, otherAction.ID)
				f.must(err)
			}

			out, err = f.turn(p, id, "respond_to_proposal", `{"decision":"confirm"}`, "Sim")
			if changed {
				code(t, err, "APPOINTMENT_CHANGED")
			} else {
				f.must(err)

				if len(out.Appointments) != 1 || out.Appointments[0].ID != original.ID {
					t.Fatalf("confirmed a different appointment: %+v", out)
				}
			}

			rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
			f.must(err)

			wantSlot := destination
			if changed {
				wantSlot = original.SlotID
			}

			if len(rows) != 1 || rows[0].SlotID != wantSlot || rows[0].DoctorID != original.DoctorID {
				t.Fatalf("restart changed the wrong appointment: %+v", rows)
			}
		})
	}
}
