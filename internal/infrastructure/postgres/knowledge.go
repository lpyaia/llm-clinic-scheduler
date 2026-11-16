package postgres

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"gorm.io/gorm"
)

var _ knowledge.Repository = (*Store)(nil)

func (s *Store) SaveDocument(ctx context.Context, doc knowledge.Document, chunks []knowledge.Chunk) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cfg := doc.EmbeddingConfig
		if err := tx.Exec(`
			INSERT INTO clinic_documents (id, source, content, provider, model, dimensions, chunk_count)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			doc.ID, doc.Source, doc.Content, cfg.Provider, cfg.Model, cfg.Dimensions, doc.ChunkCount,
		).Error; err != nil {
			return err
		}

		for _, chunk := range chunks {
			if err := cfg.ValidateVector(chunk.Vector); err != nil {
				return err
			}

			if err := tx.Exec(`
				INSERT INTO clinic_document_chunks
				(document_id, ordinal, content, start_line, end_line, dimensions, embedding)
				VALUES (?, ?, ?, ?, ?, ?, ?::public.vector)`,
				doc.ID, chunk.Ordinal, chunk.Text, chunk.StartLine, chunk.EndLine,
				cfg.Dimensions, vectorLiteral(chunk.Vector),
			).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Store) DocumentConfig(ctx context.Context, id uuid.UUID) (knowledge.EmbeddingConfig, error) {
	var cfg knowledge.EmbeddingConfig

	err := s.db.WithContext(ctx).Table("clinic_documents").
		Select("provider, model, dimensions").Where("id = ?", id).Take(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return knowledge.EmbeddingConfig{}, knowledge.ErrNotFound
	}

	return cfg, err
}

func (s *Store) SearchDocument(
	ctx context.Context, id uuid.UUID, cfg knowledge.EmbeddingConfig, vector []float32, limit int,
) ([]knowledge.Result, error) {
	if err := cfg.ValidateVector(vector); err != nil {
		return nil, err
	}

	results := make([]knowledge.Result, 0)

	err := s.db.WithContext(ctx).Raw(`
		SELECT c.document_id, d.source, c.ordinal, c.content AS text, c.start_line, c.end_line,
		       1 - (c.embedding OPERATOR(public.<=>) ?::public.vector) AS similarity
		FROM clinic_document_chunks c JOIN clinic_documents d ON d.id = c.document_id
		WHERE d.id = ? AND d.provider = ? AND d.model = ? AND d.dimensions = ?
		ORDER BY c.embedding OPERATOR(public.<=>) ?::public.vector, c.ordinal
		LIMIT ?`,
		vectorLiteral(vector), id, cfg.Provider, cfg.Model, cfg.Dimensions, vectorLiteral(vector), limit,
	).Scan(&results).Error
	if err != nil {
		return nil, err
	}

	// The SQL guard enforces compatibility without reloading metadata on successful searches.
	// Resolve an empty result explicitly so an incompatible configuration cannot pass silently.
	if len(results) == 0 {
		recorded, err := s.DocumentConfig(ctx, id)
		if err != nil {
			return nil, err
		}

		if recorded != cfg {
			return nil, knowledge.ErrIncompatible
		}
	}

	return results, nil
}

func vectorLiteral(vector []float32) string {
	values := make([]string, len(vector))
	for i, value := range vector {
		values[i] = strconv.FormatFloat(float64(value), 'g', -1, 32)
	}

	return "[" + strings.Join(values, ",") + "]"
}
