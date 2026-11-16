package evals

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// repository is scoped to a single sequential evaluation scenario, never shared with the server.
type repository struct {
	state     State
	actions   map[uuid.UUID]domain.Action
	failWrite string
}

var _ application.Repository = (*repository)(nil)

func newRepository(state State) *repository {
	state.Appointments = slices.Clone(state.Appointments)
	return &repository{state: state, actions: map[uuid.UUID]domain.Action{}}
}

// Fixture events happen between turns, before taking the baseline for agent effects.
func (r *repository) upsert(rows []domain.Appointment) {
	for _, row := range rows {
		index := slices.IndexFunc(r.state.Appointments, func(old domain.Appointment) bool { return old.ID == row.ID })
		if index < 0 {
			r.state.Appointments = append(r.state.Appointments, row)
		} else {
			r.state.Appointments[index] = row
		}
	}
}

func (r *repository) Transaction(ctx context.Context, operation func(application.Repository) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	rows, actions := slices.Clone(r.state.Appointments), maps.Clone(r.actions)
	if err := operation(r); err != nil {
		r.state.Appointments, r.actions = rows, actions
		return err
	}

	return nil
}

func (*repository) LockPatient(ctx context.Context, _ uuid.UUID) error { return ctx.Err() }

func (r *repository) Specialties(ctx context.Context) ([]domain.Specialty, error) {
	return slices.Clone(r.state.Specialties), ctx.Err()
}

func (r *repository) Doctors(ctx context.Context, specialty uuid.UUID) ([]domain.Doctor, error) {
	rows := []domain.Doctor{}

	for _, doctor := range r.state.Doctors {
		if specialty == uuid.Nil || doctor.SpecialtyID == specialty {
			rows = append(rows, doctor)
		}
	}

	return rows, ctx.Err()
}

func (r *repository) Slots(ctx context.Context, filter application.SlotFilter) ([]domain.Slot, error) {
	location, err := time.LoadLocation(filter.Timezone)
	if err != nil {
		return nil, err
	}

	rows := []domain.Slot{}

	for _, slot := range r.state.Slots {
		hour := slot.StartsAt.In(location).Hour()
		if !slot.StartsAt.After(filter.Now) || slot.StartsAt.Before(filter.From) ||
			!slot.StartsAt.Before(filter.To) || hour < filter.MinHour || hour >= filter.MaxHour ||
			(filter.DoctorID != uuid.Nil && filter.DoctorID != slot.DoctorID) {
			continue
		}

		if filter.SpecialtyID != uuid.Nil && !slices.ContainsFunc(r.state.Doctors, func(doctor domain.Doctor) bool {
			return doctor.ID == slot.DoctorID && doctor.SpecialtyID == filter.SpecialtyID
		}) {
			continue
		}

		occupied, _ := r.SlotOccupied(ctx, slot.ID, uuid.Nil)
		if !occupied {
			rows = append(rows, slot)
		}
	}

	slices.SortFunc(rows, func(a, b domain.Slot) int {
		if order := a.StartsAt.Compare(b.StartsAt); order != 0 {
			return order
		}

		return slices.Compare(a.ID[:], b.ID[:])
	})

	return rows, ctx.Err()
}

func (r *repository) Slot(ctx context.Context, id uuid.UUID, _ bool) (domain.Slot, error) {
	for _, slot := range r.state.Slots {
		if slot.ID == id {
			return slot, ctx.Err()
		}
	}

	return domain.Slot{}, domain.ErrNotFound
}

func (r *repository) SlotOccupied(ctx context.Context, slot, except uuid.UUID) (bool, error) {
	for _, row := range r.state.Appointments {
		if row.SlotID == slot && row.ID != except && row.Status != domain.Cancelled {
			return true, ctx.Err()
		}
	}

	return false, ctx.Err()
}

func (r *repository) CountWeek(
	ctx context.Context, patient uuid.UUID, from, to time.Time, except uuid.UUID,
) (int64, error) {
	var count int64

	for _, row := range r.state.Appointments {
		if row.PatientID == patient && row.ID != except && row.Status != domain.Cancelled &&
			!row.StartsAt.Before(from) && row.StartsAt.Before(to) {
			count++
		}
	}

	return count, ctx.Err()
}

func (r *repository) Appointments(
	ctx context.Context, patient uuid.UUID, filter application.AppointmentFilter,
) ([]domain.Appointment, error) {
	out := []domain.Appointment{}

	for _, row := range r.state.Appointments {
		if row.PatientID != patient || (filter.Status != "" && filter.Status != row.Status) ||
			(!filter.From.IsZero() && row.StartsAt.Before(filter.From)) ||
			(!filter.To.IsZero() && !row.StartsAt.Before(filter.To)) {
			continue
		}

		out = append(out, row)
	}

	return out, ctx.Err()
}

func (r *repository) Appointment(
	ctx context.Context, id, patient uuid.UUID, _ bool,
) (domain.Appointment, error) {
	for _, row := range r.state.Appointments {
		if row.ID == id && row.PatientID == patient {
			return row, ctx.Err()
		}
	}

	return domain.Appointment{}, domain.ErrNotFound
}

func (r *repository) InsertAppointment(ctx context.Context, row domain.Appointment) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.state.Appointments = append(r.state.Appointments, row)

	return r.writeFailure("insert_appointment")
}

func (r *repository) UpdateAppointment(ctx context.Context, row domain.Appointment, version int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	for i, old := range r.state.Appointments {
		if old.ID == row.ID && old.PatientID == row.PatientID && old.Version == version {
			r.state.Appointments[i] = row
			return r.writeFailure("update_appointment")
		}
	}

	return domain.ErrAppointmentChanged
}

// Fail after changing the simulated rows so the core's transaction must restore them.
// Consuming the fault is not transactional: a later attempt can succeed.
func (r *repository) writeFailure(operation string) error {
	if r.failWrite != operation {
		return nil
	}

	r.failWrite = ""

	return errors.New("simulated transient appointment write failure")
}

func (r *repository) InsertAction(ctx context.Context, action domain.Action) error {
	return r.SaveAction(ctx, action)
}

func (r *repository) SaveAction(ctx context.Context, action domain.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.actions[action.ID] = action

	return nil
}

func (r *repository) Action(ctx context.Context, id, patient uuid.UUID, _ bool) (domain.Action, error) {
	if action, exists := r.actions[id]; exists && action.PatientID == patient {
		return action, ctx.Err()
	}

	return domain.Action{}, domain.ErrNotFound
}

var errIdentity = errors.New("evaluation uses a fixture principal; session operations are unavailable")

func (*repository) PatientByPhone(context.Context, string) (domain.Patient, error) {
	return domain.Patient{}, errIdentity
}

func (*repository) InsertPatient(context.Context, domain.Patient) error { return errIdentity }
func (*repository) InsertSession(context.Context, domain.Session) error { return errIdentity }

func (*repository) Session(context.Context, string) (domain.Session, error) {
	return domain.Session{}, errIdentity
}
