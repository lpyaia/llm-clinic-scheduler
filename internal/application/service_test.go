package application

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type searchRepo struct {
	Repository
	filters     []SlotFilter
	availableAt time.Time
}

func (r *searchRepo) Slots(_ context.Context, f SlotFilter) ([]domain.Slot, error) {
	r.filters = append(r.filters, f)
	if !r.availableAt.IsZero() && (r.availableAt.After(f.From) || r.availableAt.Equal(f.From)) && r.availableAt.Before(f.To) {
		return []domain.Slot{{ID: uuid.New(), StartsAt: r.availableAt}}, nil
	}

	return []domain.Slot{}, nil
}
func TestProgressiveSearch(t *testing.T) {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, loc)

	for _, test := range []struct {
		name              string
		day, calls, weeks int
		explicit          bool
	}{
		{"initial", 5, 1, 2, false}, {"expand", 22, 3, 4, false}, {"empty", 0, 7, 8, false}, {"last week", 55, 7, 8, false}, {"outside horizon", 56, 7, 8, false}, {"explicit", 22, 1, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := &searchRepo{}
			if test.day > 0 {
				r.availableAt = now.AddDate(0, 0, test.day)
			}

			s := Service{Repo: r, Clock: fixedClock{now}, Location: loc}
			in := SearchInput{DoctorID: uuid.New()}
			if test.explicit {
				in.To = now.AddDate(0, 0, 7)
			}

			out, err := s.FindSlots(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}

			if len(r.filters) != test.calls || !out.Until.Equal(now.AddDate(0, 0, test.weeks*7)) {
				t.Fatalf("calls=%d until=%s", len(r.filters), out.Until)
			}

			if out.Expanded != (test.calls > 1) {
				t.Fatalf("expanded=%v", out.Expanded)
			}
		})
	}
}
func TestSearchValidation(t *testing.T) {
	now := time.Now()
	s := Service{Clock: fixedClock{now}, Location: time.UTC}
	for _, in := range []SearchInput{
		{}, {DoctorID: uuid.Nil}, {DoctorID: uuid.New(), Period: "bad"},
		{DoctorID: uuid.New(), To: now.Add(-time.Hour)},
		{DoctorID: uuid.New(), To: now.AddDate(0, 0, 57)},
	} {
		if _, err := s.FindSlots(context.Background(), in); domain.ErrorCode(err) != "INVALID_INPUT" {
			t.Fatalf("%+v: %v", in, err)
		}
	}
}
func TestPrepareValidation(t *testing.T) {
	for _, in := range []PrepareInput{
		{}, {Operation: domain.CreateAppointment, SlotID: uuid.New(), Category: "bad"},
		{Operation: domain.CancelAppointment, AppointmentID: uuid.New(), SlotID: uuid.New()},
		{Operation: domain.RescheduleAppointment, AppointmentID: uuid.New(), SlotID: uuid.New(), Category: "exam"},
	} {
		if validatePrepare(in) == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
}
