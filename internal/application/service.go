package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

var phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func invalid(message string) error { return domain.Failure("INVALID_INPUT", message) }
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) StartSession(ctx context.Context, name, phone string) (SessionResult, error) {
	name, phone = strings.TrimSpace(name), strings.TrimSpace(phone)
	if !phonePattern.MatchString(phone) || utf8.RuneCountInString(name) > 200 {
		return SessionResult{}, invalid("Informe telefone E.164 válido e nome de até 200 caracteres.")
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return SessionResult{}, err
	}

	result := SessionResult{Token: hex.EncodeToString(b), ExpiresAt: s.Clock.Now().Add(24 * time.Hour)}
	err := s.Repo.Transaction(ctx, func(r Repository) error {
		patient, err := r.PatientByPhone(ctx, phone)
		if errors.Is(err, domain.ErrNotFound) {
			if name == "" {
				return invalid("Nome completo obrigatório para cadastrar o paciente.")
			}

			patient = domain.Patient{ID: uuid.New(), FullName: name, Phone: phone}
			if err = r.InsertPatient(ctx, patient); err != nil {
				return err
			}
			// An overlapping onboarding request may have inserted the same phone.
			patient, err = r.PatientByPhone(ctx, phone)
		}

		if err != nil {
			return err
		}

		result.Patient = patient

		return r.InsertSession(ctx, domain.Session{TokenHash: tokenHash(result.Token), PatientID: patient.ID, ExpiresAt: result.ExpiresAt})
	})

	return result, err
}

func (s *Service) Authenticate(ctx context.Context, token string) (domain.Principal, error) {
	if len(token) != 64 {
		return domain.Principal{}, domain.ErrUnauthorized
	}

	session, err := s.Repo.Session(ctx, tokenHash(token))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Principal{}, domain.ErrUnauthorized
	}

	if err != nil {
		return domain.Principal{}, err
	}

	now := s.Clock.Now()
	if now.After(session.ExpiresAt) || now.Equal(session.ExpiresAt) {
		return domain.Principal{}, domain.ErrUnauthorized
	}

	return domain.Principal{PatientID: session.PatientID, IdentityVerified: false}, nil
}

func (s *Service) FindSlots(ctx context.Context, input SearchInput) (SearchResult, error) {
	if input.DoctorID == uuid.Nil && input.SpecialtyID == uuid.Nil {
		return SearchResult{}, invalid("Informe doctor_id ou specialty_id.")
	}

	minHour, maxHour, err := domain.PeriodBounds(input.Period)
	if err != nil {
		return SearchResult{}, err
	}

	now := s.Clock.Now()
	from := input.From
	if from.IsZero() || from.Before(now) {
		from = now
	}

	from = from.In(s.Location)
	maximum := from.AddDate(0, 0, 56)
	if !input.To.IsZero() {
		if input.To.Before(from) || input.To.Equal(from) || input.To.After(maximum) {
			return SearchResult{}, invalid("O intervalo deve ser positivo e de no máximo oito semanas.")
		}
	}

	result := SearchResult{From: from}
	for weeks := 2; weeks <= 8; weeks++ {
		until := from.AddDate(0, 0, weeks*7)
		if !input.To.IsZero() {
			until = input.To
		}

		slots, err := s.Repo.Slots(ctx, SlotFilter{DoctorID: input.DoctorID, SpecialtyID: input.SpecialtyID, From: from, To: until, Now: now, MinHour: minHour, MaxHour: maxHour, Timezone: s.Location.String()})
		if err != nil {
			return SearchResult{}, err
		}

		result.Slots, result.Until, result.Expanded = slots, until, input.To.IsZero() && weeks > 2
		if len(slots) > 0 || !input.To.IsZero() {
			return result, nil
		}
	}

	return result, nil
}

func (s *Service) ListAppointments(ctx context.Context, p domain.Principal, f AppointmentFilter) ([]domain.Appointment, error) {
	if p.PatientID == uuid.Nil {
		return nil, domain.ErrUnauthorized
	}

	if f.Status != "" && !domain.ValidStatus(f.Status) {
		return nil, invalid("Status inválido.")
	}

	if !f.From.IsZero() && !f.To.IsZero() {
		if f.To.Before(f.From) || f.To.Equal(f.From) {
			return nil, invalid("Intervalo inválido.")
		}
	}

	return s.Repo.Appointments(ctx, p.PatientID, f)
}

func validatePrepare(in PrepareInput) error {
	switch in.Operation {
	case domain.CreateAppointment:
		if in.SlotID == uuid.Nil || !domain.ValidCategory(in.Category) || in.AppointmentID != uuid.Nil {
			return invalid("Informe slot_id e category para criar.")
		}
	case domain.RescheduleAppointment:
		if in.SlotID == uuid.Nil || in.AppointmentID == uuid.Nil || in.Category != "" {
			return invalid("Informe appointment_id e slot_id para remarcar.")
		}
	case domain.CancelAppointment:
		if in.AppointmentID == uuid.Nil || in.SlotID != uuid.Nil || in.Category != "" {
			return invalid("Informe somente appointment_id para cancelar.")
		}
	default:
		return invalid("Operação inválida.")
	}

	return nil
}

func (s *Service) Prepare(ctx context.Context, p domain.Principal, in PrepareInput) (domain.Action, error) {
	if p.PatientID == uuid.Nil {
		return domain.Action{}, domain.ErrUnauthorized
	}

	if err := validatePrepare(in); err != nil {
		return domain.Action{}, err
	}

	now := s.Clock.Now()
	a := domain.Action{ID: uuid.New(), PatientID: p.PatientID, Operation: in.Operation, SlotID: in.SlotID, AppointmentID: in.AppointmentID, Category: in.Category, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), Status: domain.Pending}
	err := s.Repo.Transaction(ctx, func(r Repository) error {
		appointment, slot, err := s.validate(ctx, r, a, false, now)
		if err != nil {
			return err
		}

		a.ExpectedVersion = appointment.Version
		doctor, start := slot.DoctorName, slot.StartsAt

		verb := "Agendar"
		if a.Operation == domain.RescheduleAppointment {
			verb = "Remarcar"
		}

		if a.Operation == domain.CancelAppointment {
			verb, doctor, start = "Cancelar", appointment.DoctorName, appointment.StartsAt
		}

		a.Summary = fmt.Sprintf("%s atendimento com %s em %s?", verb, doctor, start.In(s.Location).Format("02/01/2006 às 15:04"))

		return r.InsertAction(ctx, a)
	})

	return a, err
}

func (s *Service) GetAction(ctx context.Context, p domain.Principal, id uuid.UUID) (domain.Action, error) {
	if p.PatientID == uuid.Nil {
		return domain.Action{}, domain.ErrUnauthorized
	}

	if id == uuid.Nil {
		return domain.Action{}, invalid("Identificador inválido.")
	}

	return s.Repo.Action(ctx, id, p.PatientID, false)
}

func (s *Service) Confirm(ctx context.Context, p domain.Principal, id uuid.UUID) (domain.Appointment, error) {
	if p.PatientID == uuid.Nil {
		return domain.Appointment{}, domain.ErrUnauthorized
	}

	if id == uuid.Nil {
		return domain.Appointment{}, invalid("Identificador inválido.")
	}

	var (
		result      domain.Appointment
		businessErr error
	)

	err := s.Repo.Transaction(ctx, func(r Repository) error {
		if err := r.LockPatient(ctx, p.PatientID); err != nil {
			return err
		}

		a, err := r.Action(ctx, id, p.PatientID, true)
		if err != nil {
			return err
		}

		if a.Status == domain.Succeeded {
			result = *a.Result
			return nil
		}

		if a.Status == domain.Failed {
			businessErr = actionFailure(a.FailureCode)
			return nil
		}

		now := s.Clock.Now()
		if now.Equal(a.ExpiresAt) || now.After(a.ExpiresAt) {
			businessErr = domain.ErrActionExpired
		} else {
			// A savepoint rolls back a failed write before the outer transaction
			// records a terminal business failure on the pending action.
			err = r.Transaction(ctx, func(tx Repository) error {
				appointment, slot, err := s.validate(ctx, tx, a, true, now)
				if err != nil {
					return err
				}

				if a.Operation == domain.CreateAppointment {
					result = domain.Appointment{ID: uuid.New(), PatientID: p.PatientID, SlotID: slot.ID, DoctorID: slot.DoctorID, DoctorName: slot.DoctorName, StartsAt: slot.StartsAt, Category: a.Category, Status: domain.Scheduled, Version: 1}
					return tx.InsertAppointment(ctx, result)
				}

				result = appointment
				result.Version++
				if a.Operation == domain.CancelAppointment {
					result.Status = domain.Cancelled
				} else {
					result.SlotID, result.DoctorID, result.DoctorName, result.StartsAt = slot.ID, slot.DoctorID, slot.DoctorName, slot.StartsAt
				}

				return tx.UpdateAppointment(ctx, result, appointment.Version)
			})
			if err != nil {
				var failure *domain.Error
				if !errors.As(err, &failure) {
					return err
				}

				businessErr = err
			}
		}

		if businessErr != nil {
			a.Status, a.FailureCode = domain.Failed, domain.ErrorCode(businessErr)
		} else {
			a.Status, a.Result = domain.Succeeded, &result
		}

		return r.SaveAction(ctx, a)
	})
	if err != nil {
		return domain.Appointment{}, err
	}

	if businessErr != nil {
		return domain.Appointment{}, businessErr
	}

	return result, nil
}

func actionFailure(code string) error {
	switch code {
	case "ACTION_EXPIRED":
		return domain.ErrActionExpired
	case "SLOT_UNAVAILABLE":
		return domain.ErrSlotUnavailable
	case "WEEKLY_LIMIT_REACHED":
		return domain.ErrWeeklyLimit
	case "APPOINTMENT_CHANGED":
		return domain.ErrAppointmentChanged
	case "NOT_FOUND":
		return domain.ErrNotFound
	case "APPOINTMENT_NOT_MUTABLE":
		return domain.Failure(code, "Somente agendamentos futuros e ativos podem ser alterados.")
	default:
		return domain.Failure(code, "A operação não pode ser executada. Prepare uma nova confirmação.")
	}
}

func (s *Service) validate(ctx context.Context, r Repository, a domain.Action, lock bool, now time.Time) (domain.Appointment, domain.Slot, error) {
	var appointment domain.Appointment

	if a.Operation != domain.CreateAppointment {
		var err error

		appointment, err = r.Appointment(ctx, a.AppointmentID, a.PatientID, lock)
		if err != nil {
			return appointment, domain.Slot{}, err
		}

		if lock {
			now = s.Clock.Now()
			if now.After(a.ExpiresAt) || now.Equal(a.ExpiresAt) {
				return appointment, domain.Slot{}, domain.ErrActionExpired
			}
		}

		if lock && appointment.Version != a.ExpectedVersion {
			return appointment, domain.Slot{}, domain.ErrAppointmentChanged
		}

		if err := domain.ValidateChange(appointment, now); err != nil {
			return appointment, domain.Slot{}, err
		}
	}

	if a.Operation == domain.CancelAppointment {
		return appointment, domain.Slot{}, nil
	}

	slot, err := r.Slot(ctx, a.SlotID, lock)
	if err != nil {
		return appointment, slot, err
	}

	if lock {
		now = s.Clock.Now()
		if now.After(a.ExpiresAt) || now.Equal(a.ExpiresAt) {
			return appointment, slot, domain.ErrActionExpired
		}

		if a.Operation == domain.RescheduleAppointment {
			if err := domain.ValidateChange(appointment, now); err != nil {
				return appointment, slot, err
			}
		}
	}

	if slot.StartsAt.Before(now) || slot.StartsAt.Equal(now) {
		return appointment, slot, domain.ErrSlotUnavailable
	}

	if a.Operation == domain.RescheduleAppointment && (slot.DoctorID != appointment.DoctorID || slot.ID == appointment.SlotID) {
		return appointment, slot, invalid("Escolha outro horário do mesmo médico.")
	}

	start, end := domain.WeekBounds(slot.StartsAt, s.Location)

	count, err := r.CountWeek(ctx, a.PatientID, start, end, appointment.ID)
	if err != nil {
		return appointment, slot, err
	}

	if count >= 3 {
		return appointment, slot, domain.ErrWeeklyLimit
	}

	occupied, err := r.SlotOccupied(ctx, slot.ID, appointment.ID)
	if err != nil {
		return appointment, slot, err
	}

	if occupied {
		return appointment, slot, domain.ErrSlotUnavailable
	}

	return appointment, slot, nil
}
