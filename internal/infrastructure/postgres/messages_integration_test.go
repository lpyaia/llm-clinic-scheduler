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
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
)

type messageOutcome struct {
	result agent.Result
	err    error
}

func sameMessageResult(t *testing.T, first, second agent.Result) bool {
	t.Helper()

	left, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}

	right, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}

	return string(left) == string(right)
}

func TestIdentifiedMessagesSerializeAcrossInstancesWithoutLosingState(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	afternoon := f.slot(1, 14)
	f.slot(1, 9)
	seed, err := f.turn(p, uuid.Nil, "find_available_slots", `{}`, "Olá")
	f.must(err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	entered, release := make(chan struct{}), make(chan struct{})
	first, _ := f.assistant(conversationModel(func(ctx context.Context, _ agent.Request) (agent.Response, error) {
		close(entered)

		select {
		case <-release:
			return conversationTool("find_available_slots", `{"specialty":"Cardiologia"}`), nil
		case <-ctx.Done():
			return agent.Response{}, ctx.Err()
		}
	}))
	secondRequests := make(chan agent.Request, 1)
	second, secondStore := f.assistant(
		conversationModel(func(_ context.Context, in agent.Request) (agent.Response, error) {
			if in.Messages[len(in.Messages)-1].Content == "Prefiro à tarde" {
				secondRequests <- in
				return conversationTool("find_available_slots", `{"period":"afternoon"}`), nil
			}

			return conversationTool("find_available_slots", `{}`), nil
		}),
	)
	input := agent.MessageInput{ID: uuid.New(), ConversationID: seed.ConversationID, Text: "Quero cardiologia"}
	firstDone, duplicateDone, secondDone := make(chan messageOutcome, 1), make(chan messageOutcome, 1),
		make(chan messageOutcome, 1)

	go func() {
		out, err := first.ReplyMessage(ctx, p, input)
		firstDone <- messageOutcome{out, err}
	}()

	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first instance did not start")
	}

	go func() {
		out, err := second.ReplyMessage(ctx, p, input)
		duplicateDone <- messageOutcome{out, err}
	}()
	go func() {
		out, err := second.ReplyMessage(ctx, p, agent.MessageInput{
			ID: uuid.New(), ConversationID: seed.ConversationID, Text: "Prefiro à tarde",
		})
		secondDone <- messageOutcome{out, err}
	}()

	// A distinct conversation must not wait for the blocked model of this one.
	_, err = second.ReplyMessage(ctx, p, agent.MessageInput{ID: uuid.New(), Text: "Outra conversa"})
	f.must(err)

	select {
	case <-secondRequests:
		t.Fatal("second instance interpreted while the first still held access")
	case <-time.After(30 * time.Millisecond):
	}

	close(release)

	firstOut, duplicate, secondOut := <-firstDone, <-duplicateDone, <-secondDone
	f.must(firstOut.err)
	f.must(duplicate.err)
	f.must(secondOut.err)

	if !sameMessageResult(t, firstOut.result, duplicate.result) {
		t.Fatalf("duplicate changed the original response: %+v %+v", firstOut, duplicate)
	}

	if secondOut.result.Availability == nil || len(secondOut.result.Availability.Slots) != 1 ||
		secondOut.result.Availability.Slots[0].ID != afternoon {
		t.Fatalf("concurrent refinement lost the first message's preferences: %+v", secondOut)
	}

	request := <-secondRequests
	if len(request.Messages) != 6 || request.Messages[3].Content != input.Text {
		t.Fatalf("second message did not see the committed first exchange: %+v", request.Messages)
	}

	locked, err := secondStore.Acquire(ctx, p.PatientID, seed.ConversationID, agent.ConversationAccess{})
	f.must(err)

	defer locked.Release()

	state := locked.Snapshot()
	if len(state.Messages) != 2 || len(state.History) != 6 || state.Messages[0].ID != input.ID ||
		state.Messages[0].Status != agent.MessageCompleted || state.Messages[1].Status != agent.MessageCompleted {
		t.Fatalf("messages were lost or repeated: %+v", state)
	}
}

func TestMessageConfirmationReplayAfterRestartDoesNotMutateAgain(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	slot := f.slot(1, 14)
	proposal := f.conversationProposal(p, slot)
	input := agent.MessageInput{ID: uuid.New(), ConversationID: proposal.ConversationID,
		Revision: proposal.Revision, Text: "Sim"}
	a, store := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool("respond_to_proposal", `{"decision":"confirm"}`), nil
	}))
	out, err := a.ReplyMessage(context.Background(), p, input)
	f.must(err)
	f.must(store.Close())

	if len(out.Appointments) != 1 {
		t.Fatalf("missing confirmed appointment: %+v", out)
	}

	restarted, _ := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{}, errors.New("model must not run for replay")
	}))
	replayed, err := restarted.ReplyMessage(context.Background(), p, input)
	f.must(err)

	if !sameMessageResult(t, out, replayed) {
		t.Fatalf("replay changed after restarting both pools: %+v %+v", out, replayed)
	}

	rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
	f.must(err)

	if len(rows) != 1 || rows[0].SlotID != slot {
		t.Fatalf("replay repeated the mutation: %+v", rows)
	}
}

func TestIdentifiedMessageReconcilesCommittedConfirmationAfterInterruption(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	proposal := f.conversationProposal(p, f.slot(1, 14))
	input := agent.MessageInput{ID: uuid.New(), ConversationID: proposal.ConversationID,
		Revision: proposal.Revision, Text: "Sim"}
	// Reject the final write after the core has committed its action and appointment.
	f.must(f.store.db.Exec(`CREATE FUNCTION reject_message_result() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'interrupted final write'; END $$`).Error)
	f.must(f.store.db.Exec(`CREATE TRIGGER fail_message_result BEFORE UPDATE ON conversations
		FOR EACH ROW WHEN (NEW.state -> 'State' -> 'Completed' <> 'null'::jsonb)
		EXECUTE FUNCTION reject_message_result()`).Error)
	a, firstStore := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool("respond_to_proposal", `{"decision":"confirm"}`), nil
	}))

	out, err := a.ReplyMessage(context.Background(), p, input)
	if err == nil || out.Message != "" {
		t.Fatalf("interruption reported success: %+v err=%v", out, err)
	}

	f.must(firstStore.Close())
	f.must(f.store.db.Exec("DROP TRIGGER fail_message_result ON conversations").Error)

	restarted, _ := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{}, errors.New("recovery must query the action without running the model")
	}))
	out, err = restarted.ReplyMessage(context.Background(), p, input)
	f.must(err)

	if out.MessageID != input.ID || len(out.Appointments) != 1 ||
		!strings.Contains(out.Message, "confirmação anterior") {
		t.Fatalf("lost action outcome after restart: %+v", out)
	}

	replayed, err := restarted.ReplyMessage(context.Background(), p, input)
	f.must(err)

	if !sameMessageResult(t, replayed, out) {
		t.Fatalf("recovered result was not saved for replay: %+v", replayed)
	}

	rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
	f.must(err)

	if len(rows) != 1 {
		t.Fatalf("recovery repeated a mutation: %+v", rows)
	}
}

func TestConcurrentConfirmationCannotBindAProposalPreparedWhileWaiting(t *testing.T) {
	for _, existingProposal := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace_proposal=%t", existingProposal), func(t *testing.T) {
			f := setup(t)
			p, _ := f.patient()
			slot := f.slot(1, 14)
			seed, err := f.turn(p, uuid.Nil, "find_available_slots", `{"doctor":"Ana"}`, "Horários com Ana")
			f.must(err)

			if existingProposal {
				seed, err = f.turn(p, seed.ConversationID, "create_appointment",
					fmt.Sprintf(`{"slot_id":%q,"category":"consultation"}`, slot), "Primeiro para consulta")
				f.must(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			entered, release := make(chan struct{}), make(chan struct{})
			preparing, _ := f.assistant(
				conversationModel(func(ctx context.Context, _ agent.Request) (agent.Response, error) {
					close(entered)

					select {
					case <-release:
						return conversationTool(
							"create_appointment",
							fmt.Sprintf(`{"slot_id":%q,"category":"exam"}`, slot),
						), nil
					case <-ctx.Done():
						return agent.Response{}, ctx.Err()
					}
				}),
			)
			confirming, _ := f.assistant(
				conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
					return conversationTool("respond_to_proposal", `{"decision":"confirm"}`), nil
				}),
			)
			prepared, confirmed := make(chan messageOutcome, 1), make(chan messageOutcome, 1)

			go func() {
				out, err := preparing.ReplyMessage(ctx, p, agent.MessageInput{
					ID:             uuid.New(),
					ConversationID: seed.ConversationID,
					Revision:       seed.Revision,
					Text:           "O primeiro para exame",
				})
				prepared <- messageOutcome{out, err}
			}()

			<-entered

			go func() {
				out, err := confirming.ReplyMessage(ctx, p, agent.MessageInput{
					ID: uuid.New(), ConversationID: seed.ConversationID, Revision: seed.Revision, Text: "Sim",
				})
				confirmed <- messageOutcome{out, err}
			}()

			close(release)

			proposal, confirmation := <-prepared, <-confirmed
			f.must(proposal.err)

			if proposal.result.PendingAction == nil {
				t.Fatalf("missing prepared proposal: %+v", proposal)
			}

			code(t, confirmation.err, "MODEL_INVALID_RESPONSE")
			rows, err := f.service.ListAppointments(ctx, p, application.AppointmentFilter{})
			f.must(err)

			if len(rows) != 0 {
				t.Fatalf("waiting confirmation acquired future proposal authority: %+v", rows)
			}

			// The same text in a new message after seeing the new revision can confirm it.
			out, err := confirming.ReplyMessage(ctx, p, agent.MessageInput{ID: uuid.New(),
				ConversationID: seed.ConversationID, Revision: proposal.result.Revision, Text: "Sim"})
			f.must(err)

			if len(out.Appointments) != 1 {
				t.Fatalf("fresh consent could not confirm the presented proposal: %+v", out)
			}
		})
	}
}

func TestInitialMessageIdentitySurvivesPostgresRestartAndIsScopedToPatient(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	other, _ := f.patient()
	input := agent.MessageInput{ID: uuid.New(), Text: "Horários"}
	a, store := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool("find_available_slots", `{}`), nil
	}))
	ctx := context.Background()
	first, err := a.ReplyMessage(ctx, p, input)
	f.must(err)
	f.must(store.Close())
	restarted, _ := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool("find_available_slots", `{}`), nil
	}))
	replayed, err := restarted.ReplyMessage(ctx, p, input)
	f.must(err)

	if !sameMessageResult(t, first, replayed) {
		t.Fatalf("initial-message retry created another conversation: %+v %+v", first, replayed)
	}

	foreign, err := restarted.ReplyMessage(ctx, other, input)
	f.must(err)

	if foreign.ConversationID == first.ConversationID {
		t.Fatal("message ID alone gave access to another patient's conversation")
	}

	input.ConversationID = first.ConversationID
	_, err = restarted.ReplyMessage(ctx, other, input)
	code(t, err, "NOT_FOUND")

	input.Text = "Outro pedido"
	_, err = restarted.ReplyMessage(ctx, p, input)
	code(t, err, "INVALID_INPUT")
}

func TestInterruptedMessageCanBeRecoveredByAnotherInstanceWithoutLosingTheNextInput(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	seed, err := f.turn(p, uuid.Nil, "find_available_slots", `{}`, "Olá")
	f.must(err)

	input := agent.MessageInput{ID: uuid.New(), ConversationID: seed.ConversationID, Text: "Mensagem interrompida"}
	a, store := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		panic("simulated interrupted instance")
	}))

	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected simulated interruption")
			}
		}()

		_, _ = a.ReplyMessage(context.Background(), p, input)
	}()
	f.must(store.Close())
	resumed, secondStore := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool("find_available_slots", `{}`), nil
	}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out, err := resumed.ReplyMessage(ctx, p, agent.MessageInput{
		ID: uuid.New(), ConversationID: seed.ConversationID, Text: "Nova mensagem",
	})
	f.must(err)

	if out.Message == "" || out.ConversationID != seed.ConversationID {
		t.Fatalf("next distinct message was not processed: %+v", out)
	}

	_, err = resumed.ReplyMessage(ctx, p, input)
	code(t, err, "MESSAGE_INTERRUPTED")
	locked, err := secondStore.Acquire(ctx, p.PatientID, seed.ConversationID, agent.ConversationAccess{})
	f.must(err)

	defer locked.Release()

	state := locked.Snapshot()
	if len(state.Messages) != 2 || state.Messages[0].Text != input.Text ||
		state.Messages[0].Status != agent.MessageFailed || state.Messages[1].Status != agent.MessageCompleted ||
		len(state.History) != 4 || state.History[2].Content != "Nova mensagem" {
		t.Fatalf("interruption lost an accepted input or its successor: %+v", state)
	}
}

func TestMessageCancelledWhileWaitingIsNotAcceptedAndCanBeRetried(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	seed, err := f.turn(p, uuid.Nil, "find_available_slots", `{}`, "Olá")
	f.must(err)

	ctx := context.Background()
	locked, err := f.store.Acquire(ctx, p.PatientID, seed.ConversationID, agent.ConversationAccess{})
	f.must(err)

	defer locked.Release()

	a, _ := f.assistant(conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
		return conversationTool("find_available_slots", `{}`), nil
	}))
	input := agent.MessageInput{ID: uuid.New(), ConversationID: seed.ConversationID, Text: "Aguardando"}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()

	_, err = a.ReplyMessage(waitCtx, p, input)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait ignored cancellation: %v", err)
	}

	if len(locked.Snapshot().Messages) != 0 {
		t.Fatal("message was accepted before obtaining exclusive access")
	}

	locked.Release()

	out, err := a.ReplyMessage(ctx, p, input)
	f.must(err)

	if out.MessageID != input.ID {
		t.Fatalf("retry could not process the previously unaccepted message: %+v", out)
	}
}
