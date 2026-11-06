package agent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// These cases exercise model arguments through the conversation, not language interpretation by a live model.
func TestSearchWeekdayAndHourRefinements(t *testing.T) {
	for _, test := range []struct {
		name, message, args, from, until string
		wantHours                        []int
	}{
		{
			name:    "on Wednesday selects only that day",
			message: "Na quarta-feira",
			args: `{"date":null,"period":null,"from":"2026-11-11T00:00:00-03:00",` +
				`"to":"2026-11-12T00:00:00-03:00","clarification_question":null}`,
			from: "2026-11-11T00:00:00-03:00", until: "2026-11-12T00:00:00-03:00",
			wantHours: []int{12, 14},
		},
		{
			name:    "exact hours replace the previous afternoon preference",
			message: "A doutora tem horário entre às 10h e 12h na próxima quarta-feira?",
			args: `{"date":null,"period":"","from":"2026-11-11T10:00:00-03:00",` +
				`"to":"2026-11-11T12:00:00-03:00","clarification_question":null}`,
			from: "2026-11-11T10:00:00-03:00", until: "2026-11-11T12:00:00-03:00",
			wantHours: []int{10, 11},
		},
		{
			name:    "after Wednesday excludes Wednesday and retains the selected week",
			message: "Depois de quarta-feira, em qualquer período",
			args:    `{"date":"after_wednesday","period":"","clarification_question":null}`,
			from:    "2026-11-12T00:00:00-03:00", until: "2026-11-16T00:00:00-03:00",
			wantHours: []int{10},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, repo := searchFixture()
			core.Clock = searchClock{time.Date(2026, 11, 6, 22, 0, 0, 0, time.UTC)}

			base := repo.slots[0]
			repo.slots = nil

			for _, dayHour := range [][2]int{
				{9, 10}, {11, 9}, {11, 10}, {11, 11}, {11, 12}, {11, 14}, {12, 10}, {16, 10},
			} {
				slot := base
				slot.ID = uuid.New()
				slot.StartsAt = time.Date(2026, 11, dayHour[0], dayHour[1], 0, 0, 0, core.Location)
				repo.slots = append(repo.slots, slot)
			}

			step := 0
			model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
				step++

				if !strings.Contains(in.Messages[0].Content, "2026-11-06T19:00:00-03:00") {
					t.Fatal("model did not receive the clinic's current local time")
				}

				args := `{"specialty":"Cardiologia","date":"next_week","period":"afternoon"}`
				if step > 1 {
					args = test.args
				}

				return toolReply("search", "find_available_slots", args), nil
			})
			a, principal := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

			first, err := a.Reply(context.Background(), principal, "Cardiologista semana que vem à tarde")
			if err != nil {
				t.Fatal(err)
			}

			out, err := a.ReplyConversation(context.Background(), principal, first.ConversationID, test.message)
			if err != nil || out.Availability == nil {
				t.Fatalf("refinement failed: result=%+v err=%v", out, err)
			}

			found := out.Availability
			if found.From.Format(time.RFC3339) != test.from || found.Until.Format(time.RFC3339) != test.until ||
				found.Expanded || len(found.Slots) != len(test.wantHours) {
				t.Fatalf("unexpected search window or slots: %+v", found)
			}

			for i, slot := range found.Slots {
				if slot.StartsAt.Hour() != test.wantHours[i] {
					t.Fatalf("slot %d: got %s, want hour %d", i, slot.StartsAt, test.wantHours[i])
				}
			}
		})
	}
}
