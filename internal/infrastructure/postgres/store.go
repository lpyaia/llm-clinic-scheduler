package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"github.com/pressly/goose/v3"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ db *gorm.DB }

var _ application.Repository = (*Store)(nil)

func Open(dsn string) (*Store, error) {
	db, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}

	pool, err := db.DB()
	if err != nil {
		return nil, err
	}

	pool.SetMaxOpenConns(12)
	pool.SetMaxIdleConns(4)
	pool.SetConnMaxLifetime(30 * time.Minute)

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	pool, err := s.db.DB()
	if err != nil {
		return err
	}

	return pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	pool, err := s.db.DB()
	if err != nil {
		return err
	}

	return pool.PingContext(ctx)
}

func (s *Store) Migrate(ctx context.Context) error {
	pool, err := s.db.DB()
	if err != nil {
		return err
	}

	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, pool, files)
	if err != nil {
		return err
	}

	_, err = provider.Up(ctx)

	return err
}

func (s *Store) Transaction(ctx context.Context, fn func(application.Repository) error) error {
	return translate(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(&Store{db: tx}) }))
}

func translate(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrNotFound
	}

	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "appointments_slot_reserved" {
		return domain.ErrSlotUnavailable
	}

	return err
}

func (s *Store) PatientByPhone(ctx context.Context, phone string) (domain.Patient, error) {
	var row patientRow

	err := s.db.WithContext(ctx).Where("phone = ?", phone).Take(&row).Error

	return domain.Patient{ID: row.ID, FullName: row.FullName, Phone: row.Phone}, translate(err)
}

func (s *Store) InsertPatient(ctx context.Context, p domain.Patient) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "phone"}}, DoNothing: true}).Create(&patientRow{ID: p.ID, FullName: p.FullName, Phone: p.Phone}).Error
}

func (s *Store) InsertSession(ctx context.Context, v domain.Session) error {
	return s.db.WithContext(ctx).Create(&sessionRow{TokenHash: v.TokenHash, PatientID: v.PatientID, ExpiresAt: v.ExpiresAt}).Error
}

func (s *Store) Session(ctx context.Context, hash string) (domain.Session, error) {
	var row sessionRow

	err := s.db.WithContext(ctx).Where("token_hash = ?", hash).Take(&row).Error

	return domain.Session{TokenHash: row.TokenHash, PatientID: row.PatientID, ExpiresAt: row.ExpiresAt}, translate(err)
}

func (s *Store) LockPatient(ctx context.Context, id uuid.UUID) error {
	var row patientRow
	return translate(s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", id).Take(&row).Error)
}

func (s *Store) Specialties(ctx context.Context) ([]domain.Specialty, error) {
	var rows []specialtyRow

	err := s.db.WithContext(ctx).Order("name, id").Find(&rows).Error
	out := make([]domain.Specialty, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Specialty{ID: row.ID, Name: row.Name})
	}

	return out, err
}

func (s *Store) Doctors(ctx context.Context, specialty uuid.UUID) ([]domain.Doctor, error) {
	var rows []doctorRow

	q := s.db.WithContext(ctx)
	if specialty != uuid.Nil {
		q = q.Where("specialty_id = ?", specialty)
	}

	err := q.Order("name, id").Find(&rows).Error
	out := make([]domain.Doctor, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Doctor{ID: row.ID, Name: row.Name, SpecialtyID: row.SpecialtyID})
	}

	return out, err
}

func (s *Store) Slots(ctx context.Context, f application.SlotFilter) ([]domain.Slot, error) {
	q := s.db.WithContext(ctx).Table("slots AS s").Select("s.id, s.doctor_id, s.starts_at, d.name AS doctor_name").Joins("JOIN doctors AS d ON d.id = s.doctor_id").
		Where("s.starts_at >= ? AND s.starts_at < ?", f.From, f.To).
		Where("s.starts_at > ?", f.Now).
		Where("NOT EXISTS (SELECT 1 FROM appointments a WHERE a.slot_id = s.id AND a.status IN ('scheduled','completed'))").
		Where("extract(hour FROM s.starts_at AT TIME ZONE ?) >= ? AND extract(hour FROM s.starts_at AT TIME ZONE ?) < ?", f.Timezone, f.MinHour, f.Timezone, f.MaxHour)
	if f.DoctorID != uuid.Nil {
		q = q.Where("s.doctor_id = ?", f.DoctorID)
	}

	if f.SpecialtyID != uuid.Nil {
		q = q.Where("d.specialty_id = ?", f.SpecialtyID)
	}

	var rows []slotRow

	err := q.Order("s.starts_at, s.id").Find(&rows).Error
	out := make([]domain.Slot, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Slot{ID: row.ID, DoctorID: row.DoctorID, DoctorName: row.DoctorName, StartsAt: row.StartsAt})
	}

	return out, err
}

func (s *Store) Slot(ctx context.Context, id uuid.UUID, lock bool) (domain.Slot, error) {
	q := s.db.WithContext(ctx).Table("slots AS s").Select("s.*, d.name AS doctor_name").Joins("JOIN doctors d ON d.id = s.doctor_id")
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "s"}})
	}

	var row slotRow

	err := q.Where("s.id = ?", id).Take(&row).Error

	return domain.Slot{ID: row.ID, DoctorID: row.DoctorID, DoctorName: row.DoctorName, StartsAt: row.StartsAt}, translate(err)
}

func (s *Store) SlotOccupied(ctx context.Context, id, exclude uuid.UUID) (bool, error) {
	q := s.db.WithContext(ctx).Model(&appointmentRow{}).Where("slot_id = ? AND status IN ('scheduled','completed')", id)
	if exclude != uuid.Nil {
		q = q.Where("id <> ?", exclude)
	}

	var count int64

	err := q.Count(&count).Error

	return count > 0, err
}

func (s *Store) CountWeek(ctx context.Context, patient uuid.UUID, from, to time.Time, exclude uuid.UUID) (int64, error) {
	q := s.db.WithContext(ctx).Table("appointments AS a").Joins("JOIN slots s ON s.id = a.slot_id").Where("a.patient_id = ? AND a.status IN ('scheduled','completed') AND s.starts_at >= ? AND s.starts_at < ?", patient, from, to)
	if exclude != uuid.Nil {
		q = q.Where("a.id <> ?", exclude)
	}

	var count int64

	err := q.Count(&count).Error

	return count, err
}

func appointmentQuery(db *gorm.DB) *gorm.DB {
	return db.Table("appointments AS a").Select("a.*, s.doctor_id, s.starts_at, d.name AS doctor_name").Joins("JOIN slots AS s ON s.id = a.slot_id").Joins("JOIN doctors d ON d.id = s.doctor_id")
}

func toAppointment(row appointmentRow) domain.Appointment {
	return domain.Appointment{ID: row.ID, PatientID: row.PatientID, SlotID: row.SlotID, DoctorID: row.DoctorID, DoctorName: row.DoctorName, StartsAt: row.StartsAt, Category: row.Category, Status: row.Status, Version: row.Version}
}

func (s *Store) Appointments(ctx context.Context, patient uuid.UUID, f application.AppointmentFilter) ([]domain.Appointment, error) {
	q := appointmentQuery(s.db.WithContext(ctx)).Where("a.patient_id = ?", patient)
	if f.Status != "" {
		q = q.Where("a.status = ?", f.Status)
	}

	if !f.From.IsZero() {
		q = q.Where("s.starts_at >= ?", f.From)
	}

	if !f.To.IsZero() {
		q = q.Where("s.starts_at < ?", f.To)
	}

	var rows []appointmentRow

	err := q.Order("s.starts_at, a.id").Find(&rows).Error
	out := make([]domain.Appointment, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAppointment(row))
	}

	return out, err
}

func (s *Store) Appointment(ctx context.Context, id, patient uuid.UUID, lock bool) (domain.Appointment, error) {
	q := appointmentQuery(s.db.WithContext(ctx)).Where("a.id = ? AND a.patient_id = ?", id, patient)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "a"}})
	}

	var row appointmentRow

	err := q.Take(&row).Error

	return toAppointment(row), translate(err)
}

func (s *Store) InsertAppointment(ctx context.Context, a domain.Appointment) error {
	return translate(s.db.WithContext(ctx).Create(&appointmentRow{ID: a.ID, PatientID: a.PatientID, SlotID: a.SlotID, Category: a.Category, Status: a.Status, Version: a.Version}).Error)
}

func (s *Store) UpdateAppointment(ctx context.Context, a domain.Appointment, expected int) error {
	q := s.db.WithContext(ctx).Model(&appointmentRow{}).Where("id = ? AND patient_id = ? AND version = ?", a.ID, a.PatientID, expected).Updates(map[string]any{"slot_id": a.SlotID, "status": a.Status, "version": a.Version})
	if q.Error != nil {
		return translate(q.Error)
	}

	if q.RowsAffected != 1 {
		return domain.ErrAppointmentChanged
	}

	return nil
}

func nullable(v string) *string {
	if v == "" {
		return nil
	}

	return &v
}

func value(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}

func nullableUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}

	return &id
}

func uuidValue(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}

	return *id
}

func (s *Store) InsertAction(ctx context.Context, a domain.Action) error {
	return s.db.WithContext(ctx).Create(&actionRow{ID: a.ID, PatientID: a.PatientID, Operation: a.Operation, SlotID: nullableUUID(a.SlotID), AppointmentID: nullableUUID(a.AppointmentID), Category: nullable(a.Category), ExpectedVersion: a.ExpectedVersion, Status: a.Status, FailureCode: a.FailureCode, Summary: a.Summary, CreatedAt: a.CreatedAt, ExpiresAt: a.ExpiresAt}).Error
}

func (s *Store) Action(ctx context.Context, id, patient uuid.UUID, lock bool) (domain.Action, error) {
	q := s.db.WithContext(ctx).Where("id = ? AND patient_id = ?", id, patient)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	var row actionRow
	if err := q.Take(&row).Error; err != nil {
		return domain.Action{}, translate(err)
	}

	a := domain.Action{ID: row.ID, PatientID: row.PatientID, Operation: row.Operation, SlotID: uuidValue(row.SlotID), AppointmentID: uuidValue(row.AppointmentID), Category: value(row.Category), ExpectedVersion: row.ExpectedVersion, Status: row.Status, FailureCode: row.FailureCode, Summary: row.Summary, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
	if row.Result != nil {
		a.Result = &domain.Appointment{}
		if err := json.Unmarshal([]byte(*row.Result), a.Result); err != nil {
			return domain.Action{}, err
		}
	}

	return a, nil
}

func (s *Store) SaveAction(ctx context.Context, a domain.Action) error {
	var result *string

	if a.Result != nil {
		b, err := json.Marshal(a.Result)
		if err != nil {
			return err
		}

		text := string(b)
		result = &text
	}

	return s.db.WithContext(ctx).Model(&actionRow{}).Where("id = ? AND patient_id = ?", a.ID, a.PatientID).Updates(map[string]any{"status": a.Status, "failure_code": a.FailureCode, "result": result}).Error
}
