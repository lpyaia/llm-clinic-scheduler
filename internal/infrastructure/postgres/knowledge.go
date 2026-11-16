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

func (s *Store) Document(ctx context.Context, id uuid.UUID) (knowledge.Document, error) {
	var row struct {
		ID                               uuid.UUID
		Source, Content, Provider, Model string
		Dimensions, ChunkCount           int
	}

	err := s.db.WithContext(ctx).Table("clinic_documents").Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return knowledge.Document{}, knowledge.ErrNotFound
	}

	return knowledge.Document{
		ID: row.ID, Source: row.Source, Content: row.Content, ChunkCount: row.ChunkCount,
		EmbeddingConfig: knowledge.EmbeddingConfig{
			Provider: row.Provider, Model: row.Model, Dimensions: row.Dimensions,
		},
	}, err
}

func (s *Store) SearchDocument(
	ctx context.Context, id uuid.UUID, cfg knowledge.EmbeddingConfig, vector []float32, limit int,
) ([]knowledge.Result, error) {
	if err := cfg.ValidateVector(vector); err != nil {
		return nil, err
	}

	doc, err := s.Document(ctx, id)
	if err != nil {
		return nil, err
	}

	if doc.EmbeddingConfig != cfg {
		return nil, knowledge.ErrIncompatible
	}

	results := make([]knowledge.Result, 0)
	err = s.db.WithContext(ctx).Raw(`
		SELECT c.document_id, d.source, c.ordinal, c.content AS text, c.start_line, c.end_line,
		       1 - (c.embedding OPERATOR(public.<=>) ?::public.vector) AS similarity
		FROM clinic_document_chunks c JOIN clinic_documents d ON d.id = c.document_id
		WHERE d.id = ? AND d.provider = ? AND d.model = ? AND d.dimensions = ?
		ORDER BY c.embedding OPERATOR(public.<=>) ?::public.vector, c.ordinal
		LIMIT ?`,
		vectorLiteral(vector), id, cfg.Provider, cfg.Model, cfg.Dimensions, vectorLiteral(vector), limit,
	).Scan(&results).Error

	return results, err
}

func vectorLiteral(vector []float32) string {
	values := make([]string, len(vector))
	for i, value := range vector {
		values[i] = strconv.FormatFloat(float64(value), 'g', -1, 32)
	}

	return "[" + strings.Join(values, ",") + "]"
}
