package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/tools"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func searchResult(t *testing.T, core *application.Service, args string) (contract.Result, error) {
	t.Helper()

	outcome, err := executor(t, core, "find_available_slots").Execute(
		context.Background(), tools.Input{Principal: domain.Principal{PatientID: uuid.New()}}, json.RawMessage(args),
	)
	if err != nil {
		return contract.Result{}, err
	}

	reply, ok := outcome.(tools.ReplyToPatient)
	if !ok {
		t.Fatalf("expected patient reply, got %T", outcome)
	}

	return reply.Result, nil
}

func TestSearchNextWeekInClinicTimezone(t *testing.T) {
	core, repo := searchFixture() // Clock is still Saturday in the clinic.

	out, err := searchResult(t, core, `{"specialty":"cardiologia","date":"next_week","period":"afternoon"}`)
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

			out, err := searchResult(t, core, args)
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
	out, err := searchResult(t, core, `{"doctor":"Ana","date":"earliest"}`)
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

			out, err := searchResult(t, core, test.args)
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
