//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
)

type conversationModel func(context.Context, agent.Request) (agent.Response, error)

func (m conversationModel) Complete(ctx context.Context, request agent.Request) (agent.Response, error) {
	return m(ctx, request)
}

func TestConversationalReschedulingPostgres(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("destination_conflict=%t", conflict), func(t *testing.T) {
			f := setup(t)
			p, _ := f.patient()
			original := f.book(p, f.slot(1, 14))
			destination := f.slot(2, 15)
			name, args := "select_appointment", `{}`
			a := agent.New(f.service, conversationModel(func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{ToolCalls: []agent.ToolCall{{
					ID: "call", Name: name, Arguments: json.RawMessage(args),
				}}}, nil
			}))
			ctx := context.Background()

			out, err := a.Reply(ctx, p, "Quero remarcar minha consulta")
			f.must(err)
			name = "find_available_slots"
			out, err = a.ReplyConversation(ctx, p, out.ConversationID, "Mostre os horários")
			f.must(err)
			if out.Availability == nil || len(out.Availability.Slots) != 1 ||
				out.Availability.Slots[0].ID != destination {
				t.Fatalf("destination search=%+v", out)
			}

			name, args = "reschedule_appointment", fmt.Sprintf(`{"slot_id":%q}`, destination)
			out, err = a.ReplyConversation(ctx, p, out.ConversationID, "O primeiro")
			if err != nil || out.PendingAction == nil || out.PendingAction.Category != "consultation" ||
				!strings.Contains(out.Message, "consulta") {
				t.Fatalf("PostgreSQL must store and present the proposal: %+v err=%v", out, err)
			}

			rows, err := f.service.ListAppointments(ctx, p, application.AppointmentFilter{})
			f.must(err)
			if len(rows) != 1 || rows[0].SlotID != original.SlotID || rows[0].Version != 1 {
				t.Fatalf("preparation changed original: %+v", rows)
			}

			if conflict {
				competitor, _ := f.patient()
				f.book(competitor, destination)
			}

			name, args = "respond_to_proposal", `{"decision":"confirm"}`
			confirmed, err := a.ReplyConversation(ctx, p, out.ConversationID, "Sim, confirmo a remarcação")
			if conflict {
				code(t, err, "SLOT_UNAVAILABLE")
				if confirmed.Message != "" {
					t.Fatalf("conflict reported success: %+v", confirmed)
				}
			} else {
				f.must(err)
				if len(confirmed.Appointments) != 1 || confirmed.Appointments[0].ID != original.ID {
					t.Fatalf("rescheduling changed ID: %+v", confirmed)
				}

				_, err = a.ReplyConversation(ctx, p, out.ConversationID, "Confirmo novamente")
				f.must(err)
			}

			rows, err = f.service.ListAppointments(ctx, p, application.AppointmentFilter{})
			f.must(err)
			wantSlot, wantVersion := destination, 2
			if conflict {
				wantSlot, wantVersion = original.SlotID, 1
			}

			if len(rows) != 1 || rows[0].ID != original.ID || rows[0].DoctorID != original.DoctorID ||
				rows[0].Category != original.Category || rows[0].SlotID != wantSlot || rows[0].Version != wantVersion {
				t.Fatalf("unexpected final reservation: %+v", rows)
			}
		})
	}
}
