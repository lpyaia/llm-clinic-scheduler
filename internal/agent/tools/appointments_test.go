package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/tools"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func TestMalformedToolArgumentsNeverReachSchedulingCore(t *testing.T) {
	for _, raw := range []string{
		`{"status":"scheduled","status":"cancelled"}`,
		`{"Status":"scheduled"}`,
		`{"patient_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"status":"invented"}`, `{"from":"yesterday"}`, `{"from":"2026-10-27T00:00:00Z","to":"2026-10-26T00:00:00Z"}`,
		`null`, `[]`, `{`, `{} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			core := &application.Service{} // Any repository access panics: invalid calls must stop before it.

			_, err := executor(t, core, "list_appointments").Execute(
				context.Background(), tools.Input{Principal: domain.Principal{PatientID: uuid.New()}},
				json.RawMessage(raw),
			)
			if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
				t.Fatalf("expected rejected arguments, got %v", err)
			}
		})
	}
}

func TestEmptyListIsReportedFaithfully(t *testing.T) {
	core := &application.Service{Repo: repository{}}

	continuation := appointmentQuery(t, core, uuid.New())
	if string(continuation.Content) != "[]" {
		t.Fatal("empty tool result must be an array")
	}

	out, err := continuation.Finalize(json.RawMessage(`{"appointment_ids":[]}`))
	if err != nil || !strings.Contains(out.Message, "não tem atendimentos") ||
		out.Appointments == nil || len(out.Appointments) != 0 {
		t.Fatalf("result=%+v error=%v", out, err)
	}
}

func TestFabricatedOrMalformedFinalAnswersAreRejected(t *testing.T) {
	for _, raw := range []string{
		`{"appointment_ids":["invented"]}`,
		`{"appointment_ids":null}`,
		`{}`,
		`null`,
		`{"appointment_ids":[],"message":"Inventado"}`,
		`{"appointment_ids":[]} {}`,
	} {
		continuation := appointmentQuery(t, &application.Service{Repo: repository{}}, uuid.New())

		_, err := continuation.Finalize(json.RawMessage(raw))
		if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
			t.Fatalf("answer=%s error=%v", raw, err)
		}
	}
}

func TestPatientListsAppointmentsInPortuguese(t *testing.T) {
	patientID := uuid.New()
	appointmentID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	loc := time.FixedZone("clinic", -3*60*60)
	core := &application.Service{Location: loc, Repo: repository{rows: []domain.Appointment{
		{
			ID:         appointmentID,
			PatientID:  patientID,
			DoctorName: "Dra. Ana",
			Category:   "consultation",
			Status:     domain.Scheduled,
			StartsAt:   time.Date(2026, 10, 26, 17, 0, 0, 0, time.UTC),
		},
		{ID: uuid.New(), PatientID: uuid.New(), DoctorName: "Outro paciente", Status: domain.Scheduled},
	}}}

	continuation := appointmentQuery(t, core, patientID)
	content := string(continuation.Content)
	if !strings.Contains(content, "Dra. Ana") || strings.Contains(content, "Outro paciente") ||
		strings.Contains(content, patientID.String()) {
		t.Fatalf("tool result must contain only this patient's appointment data: %s", content)
	}

	out, err := continuation.Finalize(
		json.RawMessage(`{"appointment_ids":["11111111-1111-4111-8111-111111111111"]}`),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, text := range []string{"Dra. Ana", "26/10/2026 às 14:00", "consulta", "agendado"} {
		if !strings.Contains(out.Message, text) {
			t.Fatalf("reply missing %q: %s", text, out.Message)
		}
	}

	if len(out.Appointments) != 1 || out.Appointments[0].ID != appointmentID {
		t.Fatalf("unexpected appointments: %+v", out)
	}
}
