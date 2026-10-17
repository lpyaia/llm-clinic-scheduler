package application

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type SlotFilter struct {
	DoctorID, SpecialtyID uuid.UUID
	From, To, Now         time.Time
	MinHour, MaxHour      int
	Timezone              string
}
type AppointmentFilter struct {
	Status   string
	From, To time.Time
}

// Repository is bound to one database handle, including within a transaction.
// Implementations must serialize mutations by patient before counting weekly bookings.
type Repository interface {
	Transaction(context.Context, func(Repository) error) error
	PatientByPhone(context.Context, string) (domain.Patient, error)
	InsertPatient(context.Context, domain.Patient) error
	InsertSession(context.Context, domain.Session) error
	Session(context.Context, string) (domain.Session, error)
	LockPatient(context.Context, uuid.UUID) error
	Specialties(context.Context) ([]domain.Specialty, error)
	Doctors(context.Context, uuid.UUID) ([]domain.Doctor, error)
	Slots(context.Context, SlotFilter) ([]domain.Slot, error)
	Slot(context.Context, uuid.UUID, bool) (domain.Slot, error)
	SlotOccupied(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	CountWeek(context.Context, uuid.UUID, time.Time, time.Time, uuid.UUID) (int64, error)
	Appointments(context.Context, uuid.UUID, AppointmentFilter) ([]domain.Appointment, error)
	Appointment(context.Context, uuid.UUID, uuid.UUID, bool) (domain.Appointment, error)
	InsertAppointment(context.Context, domain.Appointment) error
	UpdateAppointment(context.Context, domain.Appointment, int) error
	InsertAction(context.Context, domain.Action) error
	Action(context.Context, uuid.UUID, uuid.UUID, bool) (domain.Action, error)
	SaveAction(context.Context, domain.Action) error
}

type Clock interface{ Now() time.Time }
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type Service struct {
	Repo     Repository
	Clock    Clock
	Location *time.Location
}
type SessionResult struct {
	Token     string
	ExpiresAt time.Time
	Patient   domain.Patient
}
type SearchInput struct {
	DoctorID, SpecialtyID uuid.UUID
	Period                string
	From, To              time.Time
}
type SearchResult struct {
	Slots       []domain.Slot
	From, Until time.Time
	Expanded    bool
}
type PrepareInput struct {
	Operation, Category   string
	SlotID, AppointmentID uuid.UUID
}
