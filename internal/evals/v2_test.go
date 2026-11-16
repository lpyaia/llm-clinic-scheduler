package evals_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"github.com/lpyaia/llm-clinic-scheduler/internal/evals"
)

func TestEvaluationRejectsIncorrectStoredAppointmentState(t *testing.T) {
	dataset := conversationDataset(t)
	dataset.Scenarios = dataset.Scenarios[2:3]
	dataset.Scenarios[0].Turns = dataset.Scenarios[0].Turns[:3]
	dataset.Scenarios[0].Turns[2].Expect.Stored = []json.RawMessage{
		json.RawMessage(`{"Status":"cancelled","Category":"consultation"}`),
	}

	report, err := evals.Run(context.Background(), dataset, evals.Options{Mode: "scripted"})
	if err != nil {
		t.Fatal(err)
	}

	turn := report.Scenarios[0].Turns[2]
	if report.Totals.Failed != 1 || report.Totals.Errors != 0 ||
		!strings.Contains(strings.Join(turn.Failures, "\n"), "stored appointment") {
		t.Fatalf("a successful booking must fail an expectation of cancellation: %+v", turn.Failures)
	}
}

func TestStoredAppointmentSubsetsMatchRegardlessOfRowOrder(t *testing.T) {
	dataset := conversationDataset(t)
	dataset.Scenarios = dataset.Scenarios[1:2]
	scenario := &dataset.Scenarios[0]
	second := scenario.Initial.Appointments[0]
	second.ID = uuid.MustParse("50000000-0000-0000-0000-000000000003")
	second.Status = domain.Cancelled
	scenario.Initial.Appointments = append(scenario.Initial.Appointments, second)
	scenario.Turns[0].Expect.Appointments = new(2)
	scenario.Turns[0].Expect.Stored = []json.RawMessage{
		json.RawMessage(`{"PatientID":"10000000-0000-0000-0000-000000000001"}`),
		json.RawMessage(`{"ID":"50000000-0000-0000-0000-000000000002"}`),
	}

	for range 2 {
		report, err := evals.Run(context.Background(), dataset, evals.Options{Mode: "scripted"})
		if err != nil || report.Totals.Passed != 1 {
			t.Fatalf("valid overlapping subsets must match either row order: totals=%+v err=%v failures=%v",
				report.Totals, err, report.Scenarios[0].Turns[0].Failures)
		}

		slices.Reverse(scenario.Initial.Appointments)
	}
}

func TestEvaluationRevalidatesSlotAfterAnotherPatientBooksIt(t *testing.T) {
	dataset := conversationDataset(t)
	dataset.Scenarios = dataset.Scenarios[2:3]
	scenario := &dataset.Scenarios[0]
	scenario.Turns = scenario.Turns[:3]
	slot := scenario.Initial.Slots[0]
	scenario.Turns[2].UpsertAppointments = []domain.Appointment{{
		ID:        uuid.MustParse("50000000-0000-0000-0000-000000000099"),
		PatientID: uuid.MustParse("10000000-0000-0000-0000-000000000002"),
		SlotID:    slot.ID, DoctorID: slot.DoctorID, DoctorName: slot.DoctorName,
		StartsAt: slot.StartsAt, Category: "exam", Status: domain.Scheduled, Version: 1,
	}}
	scenario.Turns[2].Expect = evals.Expectations{
		Tools: scenario.Turns[2].Expect.Tools, AllowedEffects: []string{}, Appointments: new(0),
		ErrorCode: "SLOT_UNAVAILABLE", ErrorContains: []string{"nova busca"},
	}

	report, err := evals.Run(context.Background(), dataset, evals.Options{Mode: "scripted"})
	if err != nil || report.Totals.Passed != 1 || len(report.Scenarios[0].Turns[2].OtherAppointments) != 1 {
		t.Fatalf("slot conflict must preserve the competing reservation: totals=%+v err=%v", report.Totals, err)
	}
}

func TestEvaluationRejectsIncorrectAvailability(t *testing.T) {
	dataset := conversationDataset(t)
	dataset.Scenarios = dataset.Scenarios[2:3]
	dataset.Scenarios[0].Turns = dataset.Scenarios[0].Turns[:1]
	dataset.Scenarios[0].Turns[0].Expect.Response.SlotIDs = new([]uuid.UUID{})

	report, err := evals.Run(context.Background(), dataset, evals.Options{Mode: "scripted"})
	if err != nil || report.Totals.Failed != 1 || report.Totals.Errors != 0 {
		t.Fatalf("unexpected availability must fail behavior: totals=%+v err=%v", report.Totals, err)
	}
}

func TestTransientWriteFailureRequiresFreshConsentAndReplays(t *testing.T) {
	dataset := conversationDataset(t)
	dataset.Scenarios = dataset.Scenarios[2:3]
	scenario := &dataset.Scenarios[0]
	prepare, confirm := scenario.Turns[1], scenario.Turns[2]
	failure := confirm
	failure.FailWrite = "insert_appointment"
	failure.Expect = evals.Expectations{
		Tools: confirm.Expect.Tools, AllowedEffects: []string{}, Appointments: new(0),
		ErrorCode: "INTERNAL_ERROR", OperationalError: true,
		ErrorContains: []string{"Consulte seus agendamentos", "outro resumo"},
	}
	scenario.Turns = append(scenario.Turns[:2], failure, evals.Turn{
		Message: "Tente confirmar outra vez",
		Expect: evals.Expectations{
			Tools: []evals.ToolCall{}, AllowedEffects: []string{}, Appointments: new(0),
			ErrorCode: "ACTION_NOT_COMPLETED",
		},
	}, prepare, confirm)

	report, err := evals.Run(context.Background(), dataset, evals.Options{Mode: "scripted"})
	if err != nil || report.Totals.Passed != 1 {
		t.Fatalf("expected rollback and recovery with new consent: totals=%+v err=%v",
			report.Totals, err)
	}

	replayed, err := evals.Run(context.Background(), dataset, evals.Options{Mode: "replay", Recording: &report})
	if err != nil || replayed.Totals.Passed != 1 || len(replayed.Scenarios[0].Turns) != 6 {
		t.Fatalf("expected operational failure must remain inspectable and replayable: %+v err=%v", replayed, err)
	}

	scenario.Turns[2].Expect.OperationalError = false

	report, err = evals.Run(context.Background(), dataset, evals.Options{Mode: "scripted"})
	if err != nil || report.Totals.Errors != 1 {
		t.Fatalf("unexpected operational failure must not pass: totals=%+v err=%v", report.Totals, err)
	}
}

func TestEvaluationDetectsMutationsOnRefusalOrAmbiguity(t *testing.T) {
	for _, operation := range []string{"booking", "rescheduling", "cancellation"} {
		for _, decision := range []string{"reject", "clarify"} {
			t.Run(operation+"-"+decision, func(t *testing.T) {
				dataset := conversationDataset(t)
				for _, scenario := range dataset.Scenarios {
					if scenario.ID == operation+"-"+decision {
						dataset.Scenarios = []evals.Scenario{scenario}
						break
					}
				}

				if len(dataset.Scenarios) != 1 {
					t.Fatal("missing negative consent scenario")
				}

				scenario := &dataset.Scenarios[0]
				turn := &scenario.Turns[len(scenario.Turns)-1]
				turn.Responses[0].ToolCalls[0].Arguments = json.RawMessage(`{"decision":"confirm"}`)

				report, err := evals.Run(context.Background(), dataset, evals.Options{Mode: "scripted"})
				if err != nil || report.Totals.Failed != 1 || report.Totals.Errors != 0 {
					t.Fatalf("false consent must fail behavior: totals=%+v err=%v", report.Totals, err)
				}

				observed := report.Scenarios[0].Turns[len(scenario.Turns)-1]
				if !strings.Contains(strings.Join(observed.Failures, "\n"), "forbidden effect:") {
					t.Fatalf("must detect actual mutation, not just a different tool argument: %v", observed.Failures)
				}
			})
		}
	}
}

func TestEvaluationGuardsRejectUnsafeModelOutputs(t *testing.T) {
	for _, id := range []string{
		"booking-same-turn-confirmation", "confirmation-without-proposal", "identity-argument-injection",
		"booking-reject", "booking-clarify", "rescheduling-reject", "rescheduling-clarify",
		"cancellation-reject", "cancellation-clarify", "booking-search-revokes-proposal", "medical-during-proposal",
	} {
		t.Run(id, func(t *testing.T) {
			dataset := conversationDataset(t)
			for _, scenario := range dataset.Scenarios {
				if scenario.ID == id {
					dataset.Scenarios = []evals.Scenario{scenario}
					break
				}
			}

			if len(dataset.Scenarios) != 1 {
				t.Fatal("missing protocol guard scenario")
			}

			scenario := &dataset.Scenarios[0]
			last := scenario.Turns[len(scenario.Turns)-1]
			call := evals.ToolCall{
				ID: "unsafe", Name: "respond_to_proposal", Arguments: json.RawMessage(`{"decision":"confirm"}`),
			}
			unsafe := evals.Turn{
				Message:   "Confirme sem outro resumo",
				Responses: []evals.Completion{{ToolCalls: []evals.ToolCall{call}}},
				Expect: evals.Expectations{
					Tools: []evals.ToolCall{call}, AllowedEffects: []string{},
					Appointments: last.Expect.Appointments, Stored: last.Expect.Stored,
					ErrorCode: "MODEL_INVALID_RESPONSE",
				},
			}

			switch id {
			case "booking-same-turn-confirmation":
				unsafe.Responses[0].ToolCalls = append(last.Responses[0].ToolCalls, call)
				unsafe.Expect.Tools = append(last.Expect.Tools, call)
				scenario.Turns[len(scenario.Turns)-1] = unsafe
			case "confirmation-without-proposal":
				scenario.Turns[0] = unsafe
			case "identity-argument-injection":
				call.Name = "list_appointments"
				call.Arguments = json.RawMessage(`{"patient_id":"10000000-0000-0000-0000-000000000002"}`)
				unsafe.Responses[0].ToolCalls, unsafe.Expect.Tools = []evals.ToolCall{call}, []evals.ToolCall{call}
				scenario.Turns[0] = unsafe
			default:
				scenario.Turns = append(scenario.Turns, unsafe)
			}

			report, err := evals.Run(context.Background(), dataset, evals.Options{Mode: "scripted"})
			if err != nil || report.Totals.Passed != 1 {
				t.Fatalf("unsafe model output must be rejected without effects: totals=%+v err=%v", report.Totals, err)
			}
		})
	}
}
