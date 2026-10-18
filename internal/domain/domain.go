package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Error struct{ Code, Message string }

func (e *Error) Error() string            { return e.Code + ": " + e.Message }
func Failure(code, message string) *Error { return &Error{code, message} }
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}

	return "INTERNAL_ERROR"
}

var (
	ErrNotFound           = Failure("NOT_FOUND", "Recurso não encontrado.")
	ErrUnauthorized       = Failure("UNAUTHORIZED", "Sessão inválida ou expirada.")
	ErrSlotUnavailable    = Failure("SLOT_UNAVAILABLE", "Este horário não está mais disponível.")
	ErrWeeklyLimit        = Failure("WEEKLY_LIMIT_REACHED", "Você já possui três agendamentos nesta semana.")
	ErrActionExpired      = Failure("ACTION_EXPIRED", "A confirmação expirou. Prepare a operação novamente.")
	ErrAppointmentChanged = Failure("APPOINTMENT_CHANGED", "O agendamento mudou. Prepare a operação novamente.")
)

const (
	Scheduled             = "scheduled"
	Cancelled             = "cancelled"
	Completed             = "completed"
	CreateAppointment     = "create_appointment"
	RescheduleAppointment = "reschedule_appointment"
	CancelAppointment     = "cancel_appointment"
	Pending               = "pending"
	Succeeded             = "succeeded"
	Failed                = "failed"
	Duration              = 30 * time.Minute
)

type Patient struct {
	ID              uuid.UUID
	FullName, Phone string
}
type Session struct {
	TokenHash string
	PatientID uuid.UUID
	ExpiresAt time.Time
}
type Principal struct {
	PatientID        uuid.UUID
	IdentityVerified bool
}
type Specialty struct {
	ID   uuid.UUID
	Name string
}
type Doctor struct {
	ID, SpecialtyID uuid.UUID
	Name            string
}
type Slot struct {
	ID, DoctorID uuid.UUID
	DoctorName   string
	StartsAt     time.Time
}
type Appointment struct {
	ID, PatientID, SlotID, DoctorID uuid.UUID
	DoctorName, Category, Status    string
	StartsAt                        time.Time
	Version                         int
}
type Action struct {
	ID, PatientID, SlotID, AppointmentID uuid.UUID
	Operation, Category                  string
	ExpectedVersion                      int
	Status, FailureCode, Summary         string
	CreatedAt, ExpiresAt                 time.Time
	Result                               *Appointment
}

func ValidCategory(v string) bool { return v == "consultation" || v == "exam" || v == "procedure" }
func ValidStatus(v string) bool   { return v == Scheduled || v == Cancelled || v == Completed }
func WeekBounds(t time.Time, loc *time.Location) (time.Time, time.Time) {
	t = t.In(loc)
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	start = start.AddDate(0, 0, -(int(t.Weekday())+6)%7)

	return start, start.AddDate(0, 0, 7)
}

func ValidateChange(a Appointment, now time.Time) error {
	if a.Status != Scheduled || a.StartsAt.Before(now) || a.StartsAt.Equal(now) {
		return Failure("APPOINTMENT_NOT_MUTABLE", "Somente agendamentos futuros e ativos podem ser alterados.")
	}

	return nil
}

func PeriodBounds(period string) (int, int, error) {
	switch period {
	case "":
		return 0, 24, nil
	case "morning":
		return 6, 12, nil
	case "afternoon":
		return 12, 18, nil
	case "evening":
		return 18, 24, nil
	default:
		return 0, 0, Failure("INVALID_INPUT", "Período inválido: use morning, afternoon ou evening.")
	}
}
