package postgres

import (
	"time"

	"github.com/google/uuid"
)

// ORM models are deliberately confined to this package.
type patientRow struct {
	ID              uuid.UUID `gorm:"type:uuid;primaryKey"`
	FullName, Phone string
}

func (patientRow) TableName() string { return "patients" }

type sessionRow struct {
	TokenHash string    `gorm:"primaryKey"`
	PatientID uuid.UUID `gorm:"type:uuid"`
	ExpiresAt time.Time
}

func (sessionRow) TableName() string { return "sessions" }

type specialtyRow struct {
	ID   uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name string
}

func (specialtyRow) TableName() string { return "specialties" }

type doctorRow struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey"`
	SpecialtyID uuid.UUID `gorm:"type:uuid"`
	Name        string
}

func (doctorRow) TableName() string { return "doctors" }

type slotRow struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey"`
	DoctorID   uuid.UUID `gorm:"type:uuid"`
	StartsAt   time.Time
	DoctorName string `gorm:"->"`
}

func (slotRow) TableName() string { return "slots" }

type appointmentRow struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey"`
	PatientID        uuid.UUID `gorm:"type:uuid"`
	SlotID           uuid.UUID `gorm:"type:uuid"`
	Category, Status string
	Version          int
	DoctorID         uuid.UUID `gorm:"type:uuid;->"`
	DoctorName       string    `gorm:"->"`
	StartsAt         time.Time `gorm:"->"`
}

func (appointmentRow) TableName() string { return "appointments" }

type actionRow struct {
	ID                           uuid.UUID `gorm:"type:uuid;primaryKey"`
	PatientID                    uuid.UUID `gorm:"type:uuid"`
	Operation                    string
	SlotID, AppointmentID        *uuid.UUID `gorm:"type:uuid"`
	Category                     *string
	ExpectedVersion              int
	Status, FailureCode, Summary string
	Result                       *string `gorm:"type:jsonb"`
	CreatedAt, ExpiresAt         time.Time
}

func (actionRow) TableName() string { return "pending_actions" }
