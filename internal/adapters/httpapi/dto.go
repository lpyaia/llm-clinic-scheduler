package httpapi

import (
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type sessionRequest struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
}
type patientDTO struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"full_name"`
	Phone    string    `json:"phone"`
}
type sessionDTO struct {
	Token            string     `json:"token"`
	ExpiresAt        time.Time  `json:"expires_at"`
	Patient          patientDTO `json:"patient"`
	IdentityVerified bool       `json:"identity_verified"`
}
type specialtyDTO struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}
type doctorDTO struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	SpecialtyID uuid.UUID `json:"specialty_id"`
}
type slotDTO struct {
	ID              uuid.UUID `json:"id"`
	DoctorID        uuid.UUID `json:"doctor_id"`
	DoctorName      string    `json:"doctor_name"`
	StartsAt        time.Time `json:"starts_at"`
	DurationMinutes int       `json:"duration_minutes"`
}
type searchDTO struct {
	Slots         []slotDTO `json:"slots"`
	SearchedFrom  time.Time `json:"searched_from"`
	SearchedUntil time.Time `json:"searched_until"`
	Expanded      bool      `json:"expanded"`
}
type appointmentDTO struct {
	ID              uuid.UUID `json:"id"`
	PatientID       uuid.UUID `json:"patient_id"`
	SlotID          uuid.UUID `json:"slot_id"`
	DoctorID        uuid.UUID `json:"doctor_id"`
	DoctorName      string    `json:"doctor_name"`
	Category        string    `json:"category"`
	Status          string    `json:"status"`
	StartsAt        time.Time `json:"starts_at"`
	DurationMinutes int       `json:"duration_minutes"`
	Version         int       `json:"version"`
}

func appointmentResponse(a domain.Appointment, loc *time.Location) appointmentDTO {
	return appointmentDTO{ID: a.ID, PatientID: a.PatientID, SlotID: a.SlotID, DoctorID: a.DoctorID, DoctorName: a.DoctorName, Category: a.Category, Status: a.Status, StartsAt: a.StartsAt.In(loc), DurationMinutes: 30, Version: a.Version}
}

type prepareRequest struct {
	Operation     string `json:"operation"`
	SlotID        string `json:"slot_id,omitempty"`
	AppointmentID string `json:"appointment_id,omitempty"`
	Category      string `json:"category,omitempty"`
}
type actionDTO struct {
	ID            uuid.UUID       `json:"id"`
	Operation     string          `json:"operation"`
	SlotID        *uuid.UUID      `json:"slot_id,omitempty"`
	AppointmentID *uuid.UUID      `json:"appointment_id,omitempty"`
	Category      string          `json:"category,omitempty"`
	Status        string          `json:"status"`
	Summary       string          `json:"summary"`
	CreatedAt     time.Time       `json:"created_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	FailureCode   string          `json:"failure_code,omitempty"`
	Result        *appointmentDTO `json:"result,omitempty"`
}

func actionResponse(a domain.Action, loc *time.Location) actionDTO {
	out := actionDTO{ID: a.ID, Operation: a.Operation, Category: a.Category, Status: a.Status, Summary: a.Summary, CreatedAt: a.CreatedAt.In(loc), ExpiresAt: a.ExpiresAt.In(loc), FailureCode: a.FailureCode}
	if a.SlotID != uuid.Nil {
		out.SlotID = &a.SlotID
	}

	if a.AppointmentID != uuid.Nil {
		out.AppointmentID = &a.AppointmentID
	}

	if a.Result != nil {
		v := appointmentResponse(*a.Result, loc)
		out.Result = &v
	}

	return out
}

type errorDTO struct {
	Error errorDetail `json:"error"`
}
type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}
