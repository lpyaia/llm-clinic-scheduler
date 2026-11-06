package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

const (
	selectedSearchContext = `{"Doctor":"Ana","Specialty":"Cardiologia","Period":"afternoon",` +
		`"From":"2026-11-02T00:00:00-03:00","To":"2026-11-09T00:00:00-03:00",` +
		`"WindowFrom":"2026-11-02T00:00:00-03:00"}`
	emptySearchContext = `{"Doctor":"","Specialty":"","Period":"",` +
		`"From":"0001-01-01T00:00:00Z","To":"0001-01-01T00:00:00Z","WindowFrom":"0001-01-01T00:00:00Z"}`
)

func TestSearchPreferenceUpdatesPreserveModelContext(t *testing.T) {
	for _, test := range []struct {
		name, args, wantContext, question, code string
	}{
		{
			name:        "null preserves preferences",
			args:        `{"doctor":null,"specialty":null,"period":null,"date":null,"from":null,"to":null}`,
			wantContext: selectedSearchContext,
		},
		{
			name:        "empty strings clear preferences",
			args:        `{"doctor":"","specialty":"","period":"","from":"","to":""}`,
			wantContext: emptySearchContext,
			question:    "Com qual médico",
		},
		{
			name: "earliest clears selected interval",
			args: `{"date":"earliest"}`,
			wantContext: `{"Doctor":"Ana","Specialty":"Cardiologia","Period":"afternoon",` +
				`"From":"0001-01-01T00:00:00Z","To":"0001-01-01T00:00:00Z",` +
				`"WindowFrom":"0001-01-01T00:00:00Z"}`,
		},
		{
			name: "date clarification retains dates and updates other preferences",
			args: `{
				"doctor":"  Ana Souza  ","period":"morning","date":"clarify","from":"?","to":"?",
				"clarification_question":"Qual data você prefere para consultar Ana pela manhã?"
			}`,
			wantContext: `{"Doctor":"Ana Souza","Specialty":"Cardiologia","Period":"morning",` +
				`"From":"2026-11-02T00:00:00-03:00","To":"2026-11-09T00:00:00-03:00",` +
				`"WindowFrom":"2026-11-02T00:00:00-03:00"}`,
			question: "Qual data",
		},
		{
			name:        "missing clarification question discards other changes",
			args:        `{"doctor":"","period":"morning","date":"clarify"}`,
			wantContext: selectedSearchContext,
			code:        "MODEL_INVALID_RESPONSE",
		},
		{
			name:        "malformed date discards other changes",
			args:        `{"doctor":"","period":"morning","date":"tomorrow","to":"invalid"}`,
			wantContext: selectedSearchContext,
			code:        "MODEL_INVALID_RESPONSE",
		},
		{
			name:        "invalid period discards other changes",
			args:        `{"doctor":"","period":"noon","date":"earliest"}`,
			wantContext: selectedSearchContext,
			code:        "MODEL_INVALID_RESPONSE",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, _ := searchFixture()
			step := 0
			model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
				step++
				args := `{"doctor":"Ana","specialty":"Cardiologia","period":"afternoon","date":"next_week"}`
				wantContext := emptySearchContext

				switch step {
				case 2:
					args, wantContext = test.args, selectedSearchContext
				case 3:
					args, wantContext = `{}`, test.wantContext
				}

				if !strings.HasSuffix(in.Messages[0].Content, wantContext) {
					t.Fatalf("model preference context changed: %s", in.Messages[0].Content)
				}

				return agent.Response{
					ToolCalls: []agent.ToolCall{
						{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)},
					},
				}, nil
			})
			a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

			first, err := a.Reply(context.Background(), p, "Ana, cardiologia, semana que vem à tarde")
			if err != nil {
				t.Fatal(err)
			}

			out, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Alterar preferências")
			if test.code != "" {
				if domain.ErrorCode(err) != test.code {
					t.Fatalf("expected %s, got %v", test.code, err)
				}
			} else if err != nil || !strings.Contains(out.Message, test.question) {
				t.Fatalf("preference update=%+v err=%v", out, err)
			}

			if _, err := a.ReplyConversation(
				context.Background(),
				p,
				first.ConversationID,
				"Buscar novamente",
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLookupFailurePreservesSearchPreferencesAndHistory(t *testing.T) {
	for _, lookup := range []string{"doctors", "specialties", "slots"} {
		t.Run(lookup, func(t *testing.T) {
			core, repo := searchFixture()
			step := 0
			model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
				step++
				args := `{"doctor":"Ana","specialty":"Cardiologia","period":"afternoon","date":"next_week"}`
				if step == 2 {
					args = `{"doctor":"","period":"morning","date":"after_friday"}`
				}

				if step == 3 {
					args = `{}`

					if !strings.HasSuffix(in.Messages[0].Content, selectedSearchContext) || len(in.Messages) != 4 {
						t.Fatalf("failed lookup changed conversation context: %+v", in.Messages)
					}

					for _, message := range in.Messages {
						if message.Content == "Mude para manhã depois de sexta" {
							t.Fatal("failed message was persisted")
						}
					}
				}

				return agent.Response{
					ToolCalls: []agent.ToolCall{
						{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)},
					},
				}, nil
			})
			a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

			first, err := a.Reply(context.Background(), p, "Ana, cardiologia, semana que vem à tarde")
			if err != nil {
				t.Fatal(err)
			}

			want := errors.New("lookup unavailable")

			switch lookup {
			case "doctors":
				repo.doctorsErr = want
			case "specialties":
				repo.specialtiesErr = want
			case "slots":
				repo.slotsErr = want
			}

			_, err = a.ReplyConversation(
				context.Background(),
				p,
				first.ConversationID,
				"Mude para manhã depois de sexta",
			)
			if !errors.Is(err, want) {
				t.Fatalf("expected lookup failure, got %v", err)
			}

			repo.doctorsErr, repo.specialtiesErr, repo.slotsErr = nil, nil, nil
			out, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Tente novamente")
			if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 ||
				out.Availability.Slots[0].ID != repo.slots[0].ID || out.Message != first.Message {
				t.Fatalf("retry lost original preferences: %+v err=%v", out, err)
			}
		})
	}
}
