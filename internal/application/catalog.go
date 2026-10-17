package application

import (
	"context"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func (s *Service) ListSpecialties(ctx context.Context) ([]domain.Specialty, error) {
	return s.Repo.Specialties(ctx)
}

func (s *Service) ListDoctors(ctx context.Context, specialtyID uuid.UUID) ([]domain.Doctor, error) {
	return s.Repo.Doctors(ctx, specialtyID)
}
