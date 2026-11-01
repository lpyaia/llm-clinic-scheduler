package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type searchClock struct{ now time.Time }

func (c searchClock) Now() time.Time { return c.now }

type catalogRepo struct {
	application.Repository
	doctors                              []domain.Doctor
	specialties                          []domain.Specialty
	slots                                []domain.Slot
	doctorsErr, specialtiesErr, slotsErr error
}

func (r *catalogRepo) Doctors(context.Context, uuid.UUID) ([]domain.Doctor, error) {
	return r.doctors, r.doctorsErr
}
func (r *catalogRepo) Specialties(context.Context) ([]domain.Specialty, error) {
	return r.specialties, r.specialtiesErr
}
func (r *catalogRepo) Slots(_ context.Context, f application.SlotFilter) ([]domain.Slot, error) {
	if r.slotsErr != nil {
		return nil, r.slotsErr
	}

	rows := []domain.Slot{}

	for _, slot := range r.slots {
		if f.DoctorID != uuid.Nil && slot.DoctorID != f.DoctorID {
			continue
		}

		if f.SpecialtyID != uuid.Nil {
			matches := false

			for _, doctor := range r.doctors {
				if doctor.ID == slot.DoctorID && doctor.SpecialtyID == f.SpecialtyID {
					matches = true
				}
			}

			if !matches {
				continue
			}
		}

		hour := slot.StartsAt.Hour()
		if !slot.StartsAt.Before(f.From) && slot.StartsAt.Before(f.To) && !slot.StartsAt.Before(f.Now) &&
			hour >= f.MinHour &&
			hour < f.MaxHour {
			rows = append(rows, slot)
		}
	}

	return rows, nil
}

func searchFixture() (*application.Service, *catalogRepo) {
	loc := time.FixedZone("clinic", -3*60*60)
	specialtyID, doctorID := uuid.New(), uuid.New()
	repo := &catalogRepo{
		doctors:     []domain.Doctor{{ID: doctorID, SpecialtyID: specialtyID, Name: "Dra. Ana Souza"}},
		specialties: []domain.Specialty{{ID: specialtyID, Name: "Cardiologia"}},
		slots: []domain.Slot{
			{
				ID:         uuid.New(),
				DoctorID:   doctorID,
				DoctorName: "Dra. Ana Souza",
				StartsAt:   time.Date(2026, 11, 4, 14, 0, 0, 0, loc),
			},
		},
	}

	return &application.Service{
		Repo:     repo,
		Clock:    searchClock{time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)},
		Location: loc,
	}, repo
}

func searchModel(args string) modelFunc {
	return func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)}},
		}, nil
	}
}

func TestSearchNextWeekInClinicTimezone(t *testing.T) {
	core, repo := searchFixture() // Clock is still Saturday in the clinic.
	a := agent.New(core, searchModel(`{"specialty":"cardiologia","date":"next_week","period":"afternoon"}`))

	out, err := a.Reply(
		context.Background(),
		domain.Principal{PatientID: uuid.New()},
		"Cardiologia semana que vem à tarde",
	)
	if err != nil {
		t.Fatal(err)
	}

	if out.Availability == nil || len(out.Availability.Slots) != 1 || out.Availability.Slots[0].ID != repo.slots[0].ID {
		t.Fatalf("unexpected availability: %+v", out)
	}

	if out.Availability.From.Format(time.RFC3339) != "2026-11-02T00:00:00-03:00" ||
		out.Availability.Until.Format(time.RFC3339) != "2026-11-09T00:00:00-03:00" ||
		out.Availability.Expanded ||
		!strings.Contains(out.Message, "04/11/2026 às 14:00") {
		t.Fatalf("unexpected search window or rendering: %+v", out)
	}
}

func TestRepeatingWeekdayRefinementKeepsSelectedWeek(t *testing.T) {
	core, _ := searchFixture()
	step := 0
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		step++
		args := `{"doctor":"Ana","date":"next_week"}`
		if step > 1 {
			args = `{"date":"after_tuesday"}`
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)}},
		}, nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	out, err := a.Reply(context.Background(), p, "Ana semana que vem")
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		out, err = a.ReplyConversation(context.Background(), p, out.ConversationID, "Depois de terça")
		if err != nil || out.Availability == nil ||
			out.Availability.From.Format(time.RFC3339) != "2026-11-04T00:00:00-03:00" {
			t.Fatalf("weekday refinement moved week: %+v err=%v", out, err)
		}
	}
}

func TestWeekdayRefinementDoesNotWidenExplicitInterval(t *testing.T) {
	core, repo := searchFixture()
	inside := repo.slots[0]
	inside.ID, inside.StartsAt = uuid.New(), inside.StartsAt.AddDate(0, 0, 1)
	repo.slots = append(repo.slots, inside)
	step := 0
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		step++
		args := `{"doctor":"Ana","from":"2026-11-05T00:00:00-03:00","to":"2026-11-07T00:00:00-03:00"}`
		if step > 1 {
			args = `{"date":"after_tuesday"}`
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)}},
		}, nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	first, err := a.Reply(context.Background(), p, "Ana de quinta até sábado")
	if err != nil {
		t.Fatal(err)
	}

	for _, advanceClock := range []bool{false, true} {
		if advanceClock {
			core.Clock = searchClock{time.Date(2026, 11, 5, 8, 0, 0, 0, core.Location)}
		}

		out, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Depois de terça")
		if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 ||
			out.Availability.Slots[0].ID != inside.ID ||
			out.Availability.Expanded ||
			out.Availability.Until.Format(time.RFC3339) != "2026-11-07T00:00:00-03:00" {
			t.Fatalf("refinement escaped selected interval: %+v err=%v", out, err)
		}
	}
}

func TestReplacingWeekdayPreferenceCanMoveEarlierWithinSelectedWeek(t *testing.T) {
	core, repo := searchFixture()
	later := repo.slots[0]
	later.ID, later.StartsAt = uuid.New(), later.StartsAt.AddDate(0, 0, 3)
	repo.slots = append(repo.slots, later)
	step := 0
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		step++
		args := `{"doctor":"Ana","date":"next_week"}`
		if step == 2 {
			args = `{"date":"after_friday"}`
		}

		if step == 3 {
			args = `{"date":"after_tuesday"}`
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)}},
		}, nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	out, err := a.Reply(context.Background(), p, "Ana semana que vem")
	if err != nil {
		t.Fatal(err)
	}

	out, err = a.ReplyConversation(context.Background(), p, out.ConversationID, "Depois de sexta")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 {
		t.Fatalf("Friday=%+v err=%v", out, err)
	}

	out, err = a.ReplyConversation(context.Background(), p, out.ConversationID, "Prefiro depois de terça")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 2 ||
		out.Availability.From.Format(time.RFC3339) != "2026-11-04T00:00:00-03:00" {
		t.Fatalf("replacement=%+v err=%v", out, err)
	}
}

func TestConversationRetainsAndReplacesSearchPreferences(t *testing.T) {
	core, repo := searchFixture()
	morning := repo.slots[0]
	morning.ID, morning.StartsAt = uuid.New(), morning.StartsAt.Add(-5*time.Hour)
	repo.slots = append(repo.slots, morning)
	step := 0
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		step++
		args := `{"doctor":"Ana","specialty":"Cardiologia","date":"next_week","period":"afternoon"}`
		if step == 2 {
			args = `{"date":"after_tuesday"}`

			if !strings.Contains(in.Messages[0].Content, "Ana") ||
				!strings.Contains(in.Messages[0].Content, "afternoon") ||
				len(in.Messages) < 4 {
				t.Fatal("missing structured preferences or history")
			}
		}

		if step == 3 {
			args = `{"period":"morning"}`
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)}},
		}, nil
	})
	a := agent.New(core, model)
	p := domain.Principal{PatientID: uuid.New()}

	out, err := a.ReplyConversation(context.Background(), p, uuid.Nil, "Ana, cardiologia, semana que vem à tarde")
	if err != nil {
		t.Fatal(err)
	}

	id := out.ConversationID
	if id == uuid.Nil {
		t.Fatal("missing conversation ID")
	}

	out, err = a.ReplyConversation(context.Background(), p, id, "Depois de terça")
	if err != nil || out.Availability == nil ||
		out.Availability.From.Format(time.RFC3339) != "2026-11-04T00:00:00-03:00" ||
		out.Availability.Until.Format(time.RFC3339) != "2026-11-09T00:00:00-03:00" ||
		len(out.Availability.Slots) != 1 {
		t.Fatalf("refinement=%+v error=%v", out, err)
	}

	out, err = a.ReplyConversation(context.Background(), p, id, "Prefiro de manhã")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 ||
		out.Availability.Slots[0].ID != morning.ID {
		t.Fatalf("changed preference=%+v error=%v", out, err)
	}
}

func TestDateClarificationKeepsDoctorAndPeriod(t *testing.T) {
	core, _ := searchFixture()
	step := 0
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		step++
		args := `{"doctor":"Ana","period":"afternoon","date":"clarify"}`
		if step == 2 {
			args = `{"date":"next_week"}`
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)}},
		}, nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	first, err := a.Reply(context.Background(), p, "Ana à tarde, um dia desses")
	if err != nil || !strings.Contains(first.Message, "Qual data") {
		t.Fatalf("result=%+v error=%v", first, err)
	}

	out, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Semana que vem")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 {
		t.Fatalf("lost preferences: %+v err=%v", out, err)
	}
}

func TestCatalogAmbiguityRequiresClarificationAndRetainsDates(t *testing.T) {
	core, repo := searchFixture()
	repo.doctors = append(
		repo.doctors,
		domain.Doctor{ID: uuid.New(), SpecialtyID: repo.specialties[0].ID, Name: "Dra. Ana Lima"},
	)
	step := 0
	model := modelFunc(func(context.Context, agent.Request) (agent.Response, error) {
		step++
		args := `{"doctor":"Ana","date":"next_week","period":"afternoon"}`
		if step == 2 {
			args = `{"doctor":"Doutora Ana Souza"}`
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)}},
		}, nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	first, err := a.Reply(context.Background(), p, "Ana semana que vem à tarde")
	if err != nil || first.Availability != nil || !strings.Contains(first.Message, "Ana Souza") ||
		!strings.Contains(first.Message, "Ana Lima") {
		t.Fatalf("ambiguity=%+v error=%v", first, err)
	}

	out, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Ana Souza")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 {
		t.Fatalf("clarified=%+v err=%v", out, err)
	}
}

func TestSearchRelativeDatesAndExplicitWindow(t *testing.T) {
	for _, test := range []struct {
		name, args, from, until string
		expanded                bool
	}{
		{"today", `{"date":"today"}`, "2026-10-31T22:00:00-03:00", "2026-11-01T00:00:00-03:00", false},
		{"tomorrow", `{"date":"tomorrow"}`, "2026-11-01T00:00:00-03:00", "2026-11-02T00:00:00-03:00", false},
		{"this week", `{"date":"this_week"}`, "2026-10-31T22:00:00-03:00", "2026-11-02T00:00:00-03:00", false},
		{"after Tuesday", `{"date":"after_tuesday"}`, "2026-11-04T00:00:00-03:00", "2026-12-30T00:00:00-03:00", true},
		{"earliest", `{"date":"earliest"}`, "2026-10-31T22:00:00-03:00", "2026-12-26T22:00:00-03:00", true},
		{
			"explicit",
			`{"from":"2026-11-02T00:00:00-03:00","to":"2026-11-03T00:00:00-03:00"}`,
			"2026-11-02T00:00:00-03:00",
			"2026-11-03T00:00:00-03:00",
			false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, repo := searchFixture()
			repo.slots = nil
			args := strings.Replace(test.args, `{`, `{"specialty":"Cardiologia",`, 1)

			out, err := agent.New(core, searchModel(args)).
				Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, test.name)
			if err != nil || out.Availability == nil {
				t.Fatalf("result=%+v err=%v", out, err)
			}

			found := out.Availability
			if found.From.Format(time.RFC3339) != test.from || found.Until.Format(time.RFC3339) != test.until ||
				found.Expanded != test.expanded ||
				len(found.Slots) != 0 ||
				!strings.Contains(out.Message, "Não encontrei") {
				t.Fatalf("unexpected availability: %+v", found)
			}
		})
	}
}

func TestSearchProgressiveWindowReturnsOnlyActualSlots(t *testing.T) {
	core, repo := searchFixture()

	repo.slots[0].StartsAt = time.Date(2026, 11, 24, 14, 0, 0, 0, core.Location)
	out, err := agent.New(core, searchModel(`{"doctor":"Ana","date":"earliest"}`)).
		Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Ana o mais cedo possível")
	if err != nil || out.Availability == nil || !out.Availability.Expanded ||
		out.Availability.Until.Format(time.RFC3339) != "2026-11-28T22:00:00-03:00" ||
		len(out.Availability.Slots) != 1 ||
		out.Availability.Slots[0].ID != repo.slots[0].ID {
		t.Fatalf("result=%+v err=%v", out, err)
	}
}

func TestSearchMissingUnknownNamesAndInvalidArguments(t *testing.T) {
	for _, test := range []struct{ args, question, code string }{
		{`{}`, "Com qual médico", ""},
		{`{"doctor":"Inventado"}`, "Não encontrei", ""},
		{`{"specialty":"Inexistente"}`, "Não encontrei", ""},
		{`{"doctor_id":"11111111-1111-4111-8111-111111111111"}`, "", "MODEL_INVALID_RESPONSE"},
		{`{"patient_id":"forged"}`, "", "MODEL_INVALID_RESPONSE"},
		{`{"doctor":"Ana","doctor":"Bruno"}`, "", "MODEL_INVALID_RESPONSE"},
		{`{"doctor":"Ana","date":"yesterday"}`, "", "MODEL_INVALID_RESPONSE"},
		{`{"doctor":"Ana","period":"noon"}`, "", "MODEL_INVALID_RESPONSE"},
		{`{"doctor":"Ana","from":"yesterday"}`, "", "MODEL_INVALID_RESPONSE"},
	} {
		t.Run(test.args, func(t *testing.T) {
			core, _ := searchFixture()

			out, err := agent.New(core, searchModel(test.args)).
				Reply(context.Background(), domain.Principal{PatientID: uuid.New()}, "Buscar horários")
			if test.code != "" {
				if domain.ErrorCode(err) != test.code {
					t.Fatalf("expected %s got %v", test.code, err)
				}

				return
			}

			if err != nil || out.Availability != nil || !strings.Contains(out.Message, test.question) {
				t.Fatalf("result=%+v err=%v", out, err)
			}
		})
	}
}

func TestConversationStateAndHistoryAreIsolated(t *testing.T) {
	core, _ := searchFixture()
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		last := in.Messages[len(in.Messages)-1].Content
		args := `{}`
		if last == "Ana semana que vem" {
			args = `{"doctor":"Ana","date":"next_week"}`
		}

		if last == "Nova conversa" &&
			(strings.Contains(in.Messages[0].Content, `"Doctor":"Ana"`) || len(in.Messages) != 2) {
			t.Fatal("conversation state/history leaked")
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(args)}},
		}, nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	first, err := a.Reply(context.Background(), p, "Ana semana que vem")
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.ReplyConversation(
		context.Background(),
		domain.Principal{PatientID: uuid.New()},
		first.ConversationID,
		"Invadir",
	)
	if domain.ErrorCode(err) != "NOT_FOUND" {
		t.Fatalf("cross-patient access: %v", err)
	}

	for _, patient := range []domain.Principal{p, {PatientID: uuid.New()}} {
		out, err := a.Reply(context.Background(), patient, "Nova conversa")
		if err != nil || out.Availability != nil || !strings.Contains(out.Message, "Com qual médico") {
			t.Fatalf("leaked preferences: %+v err=%v", out, err)
		}
	}

	core.Clock = searchClock{time.Date(2026, 11, 3, 12, 0, 0, 0, core.Location)}

	out, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "À tarde")
	if err != nil || out.Availability == nil ||
		out.Availability.Until.Format(time.RFC3339) != "2026-11-09T00:00:00-03:00" {
		t.Fatalf("relative date was reinterpreted: %+v err=%v", out, err)
	}
}

func TestFailedTurnDoesNotChangeConversation(t *testing.T) {
	core, _ := searchFixture()
	step := 0
	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		step++
		if step == 2 {
			return agent.Response{}, errors.New("provider unavailable")
		}

		if step == 3 {
			for _, message := range in.Messages {
				if message.Content == "Mude para manhã" {
					t.Fatal("failed message was persisted")
				}
			}

			return agent.Response{
				ToolCalls: []agent.ToolCall{
					{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(`{}`)},
				},
			}, nil
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{
				{
					ID:        "search",
					Name:      "find_available_slots",
					Arguments: json.RawMessage(`{"doctor":"Ana","period":"afternoon","date":"next_week"}`),
				},
			},
		}, nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	first, err := a.Reply(context.Background(), p, "Ana à tarde semana que vem")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = a.ReplyConversation(context.Background(), p, first.ConversationID, "Mude para manhã"); err == nil {
		t.Fatal("expected provider failure")
	}

	out, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Tente novamente")
	if err != nil || out.Availability == nil || len(out.Availability.Slots) != 1 {
		t.Fatalf("state changed after failure: %+v err=%v", out, err)
	}
}

func TestBusyConversationHonorsCancellationAndDoesNotBlockOthers(t *testing.T) {
	core, _ := searchFixture()

	entered, release := make(chan struct{}), make(chan struct{}, 1)
	defer close(release)

	model := modelFunc(func(_ context.Context, in agent.Request) (agent.Response, error) {
		if in.Messages[len(in.Messages)-1].Content == "Espere" {
			close(entered)
			<-release
		}

		return agent.Response{
			ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots", Arguments: json.RawMessage(`{}`)}},
		}, nil
	})
	a, p := agent.New(core, model), domain.Principal{PatientID: uuid.New()}

	first, err := a.Reply(context.Background(), p, "Nova")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() {
		_, err := a.ReplyConversation(context.Background(), p, first.ConversationID, "Espere")
		done <- err
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("turn did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = a.ReplyConversation(ctx, p, first.ConversationID, "Mensagem concorrente")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting turn: %v", err)
	}

	otherCtx, otherCancel := context.WithTimeout(context.Background(), time.Second)
	defer otherCancel()

	if _, err := a.Reply(otherCtx, p, "Outra conversa"); err != nil {
		t.Fatalf("other conversation blocked: %v", err)
	}

	release <- struct{}{}

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
