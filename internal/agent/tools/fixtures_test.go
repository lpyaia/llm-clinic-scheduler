package tools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/tools"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func executor(t *testing.T, core *application.Service, name string) tools.Executor {
	t.Helper()

	for _, executor := range tools.New(core, tools.Schemas{}) {
		if executor.Definition().Name == name {
			return executor
		}
	}

	t.Fatalf("tool %q not registered", name)

	return nil
}

func appointmentQuery(t *testing.T, core *application.Service, patientID uuid.UUID) tools.ReturnToModel {
	t.Helper()

	outcome, err := executor(t, core, "list_appointments").Execute(
		context.Background(), tools.Input{Principal: domain.Principal{PatientID: patientID}}, json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatal(err)
	}

	continuation, ok := outcome.(tools.ReturnToModel)
	if !ok {
		t.Fatalf("expected model continuation, got %T", outcome)
	}

	return continuation
}

type repository struct {
	application.Repository
	rows []domain.Appointment
}

func (r repository) Appointments(
	_ context.Context,
	patientID uuid.UUID,
	filter application.AppointmentFilter,
) ([]domain.Appointment, error) {
	var out []domain.Appointment

	for _, row := range r.rows {
		if row.PatientID == patientID && (filter.Status == "" || row.Status == filter.Status) {
			out = append(out, row)
		}
	}

	return out, nil
}

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
