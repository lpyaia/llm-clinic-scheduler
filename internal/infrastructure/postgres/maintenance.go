package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"gorm.io/gorm/clause"
)

// Seed is additive and safe to repeat. It never rewrites existing availability.
func (s *Store) Seed(ctx context.Context, now time.Time, loc *time.Location) error {
	return s.Transaction(ctx, func(r application.Repository) error {
		tx := r.(*Store).db.WithContext(ctx)

		for i, name := range []string{"Cardiologia", "Clínica geral"} {
			specialtyID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("clinic.specialty."+name))
			doctorID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("clinic.doctor."+name))
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&specialtyRow{ID: specialtyID, Name: name}).Error; err != nil {
				return err
			}

			var specialty specialtyRow
			if err := tx.Where("name = ?", name).Take(&specialty).Error; err != nil {
				return err
			}

			specialtyID = specialty.ID
			doctorName := []string{"Dra. Ana Silva", "Dr. Bruno Santos"}[i]
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&doctorRow{ID: doctorID, Name: doctorName, SpecialtyID: specialtyID}).Error; err != nil {
				return err
			}

			local := now.In(loc)
			for day := 1; day <= 56; day++ {
				date := local.AddDate(0, 0, day)
				if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
					continue
				}

				for _, hour := range []int{9, 10, 14, 15, 17} {
					for _, minute := range []int{0, 30} {
						start := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc)
						if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&slotRow{ID: uuid.New(), DoctorID: doctorID, StartsAt: start}).Error; err != nil {
							return err
						}
					}
				}
			}
		}

		return nil
	})
}

// CompleteDue uses the same patient-first lock order as interactive mutations.
func (s *Store) CompleteDue(ctx context.Context, now time.Time) (int64, error) {
	var patients []uuid.UUID

	err := s.db.WithContext(ctx).
		Table("appointments a").
		Distinct("a.patient_id").
		Joins("JOIN slots s ON s.id = a.slot_id").
		Where("a.status = 'scheduled' AND s.starts_at + interval '30 minutes' <= ?", now).
		Pluck("a.patient_id", &patients).
		Error
	if err != nil {
		return 0, err
	}

	var total int64

	for _, patient := range patients {
		err := s.Transaction(ctx, func(r application.Repository) error {
			if err := r.LockPatient(ctx, patient); err != nil {
				return err
			}

			q := r.(*Store).db.WithContext(ctx).Exec("UPDATE appointments SET status = 'completed', version = version + 1 WHERE patient_id = ? AND status = 'scheduled' AND slot_id IN (SELECT id FROM slots WHERE starts_at + interval '30 minutes' <= ?)", patient, now)
			total += q.RowsAffected

			return q.Error
		})
		if err != nil {
			return total, err
		}
	}

	return total, nil
}
