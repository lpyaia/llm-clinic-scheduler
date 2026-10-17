package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type uuidRepository struct {
	httpRepo
	filter application.SlotFilter
	slot   domain.Slot
	action domain.Action
}

func (r *uuidRepository) Slots(_ context.Context, filter application.SlotFilter) ([]domain.Slot, error) {
	r.filter = filter

	return []domain.Slot{r.slot}, nil
}

func (r *uuidRepository) Action(context.Context, uuid.UUID, uuid.UUID, bool) (domain.Action, error) {
	return r.action, nil
}

func TestHTTPConvertsUUIDFiltersAndPreservesJSONStringIDs(t *testing.T) {
	doctorID, specialtyID := uuid.New(), uuid.New()
	r := &uuidRepository{slot: domain.Slot{ID: uuid.New(), DoctorID: doctorID, StartsAt: clock{}.Now().Add(time.Hour)}}
	service := &application.Service{Repo: r, Clock: clock{}, Location: time.UTC}
	app := New(service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest("GET", "/v1/slots?doctor_id="+strings.ToUpper(doctorID.String())+"&specialty_id="+specialtyID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()

	if resp.StatusCode != 200 || r.filter.DoctorID != doctorID || r.filter.SpecialtyID != specialtyID {
		t.Fatalf("status=%d filter=%+v", resp.StatusCode, r.filter)
	}

	var body struct {
		Slots []struct {
			ID       string `json:"id"`
			DoctorID string `json:"doctor_id"`
		} `json:"slots"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if len(body.Slots) != 1 || body.Slots[0].ID != r.slot.ID.String() || body.Slots[0].DoctorID != doctorID.String() {
		t.Fatalf("UUIDs did not remain canonical JSON strings: %+v", body)
	}
}

func TestHTTPActionOmitsAbsentUUIDs(t *testing.T) {
	for _, test := range []struct {
		operation             string
		slotID, appointmentID uuid.UUID
	}{
		{operation: domain.CreateAppointment, slotID: uuid.New()},
		{operation: domain.RescheduleAppointment, slotID: uuid.New(), appointmentID: uuid.New()},
		{operation: domain.CancelAppointment, appointmentID: uuid.New()},
	} {
		t.Run(test.operation, func(t *testing.T) {
			action := domain.Action{ID: uuid.New(), Operation: test.operation, SlotID: test.slotID, AppointmentID: test.appointmentID, Status: domain.Pending}
			r := &uuidRepository{action: action}
			service := &application.Service{Repo: r, Clock: clock{}, Location: time.UTC}
			app := New(service, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			req := httptest.NewRequest("GET", "/v1/pending-actions/"+action.ID.String(), nil)
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))

			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Error(err)
				}
			}()

			if resp.StatusCode != 200 {
				t.Fatalf("status=%d", resp.StatusCode)
			}

			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}

			for field, expected := range map[string]uuid.UUID{"slot_id": test.slotID, "appointment_id": test.appointmentID} {
				actual, present := body[field]
				if expected == uuid.Nil {
					if present {
						t.Fatalf("absent %s was serialized: %v", field, actual)
					}

					continue
				}

				if actual != expected.String() {
					t.Fatalf("%s: want %s, got %v", field, expected, actual)
				}
			}
		})
	}
}
