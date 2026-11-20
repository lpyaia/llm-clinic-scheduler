package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"gorm.io/gorm"
)

var _ knowledge.Repository = (*Store)(nil)

func (s *Store) SaveDocument(ctx context.Context, doc knowledge.Document, chunks []knowledge.Chunk) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize corpus configuration checks and imports, including concurrent first documents.
		// Ordinary searches remain available; external embedding calls happen before this transaction.
		if err := tx.Exec("LOCK TABLE clinic_documents IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
			return err
		}

		recorded, err := corpusConfig(tx)
		if err != nil {
			return err
		}

		cfg := doc.EmbeddingConfig
		if recorded != nil && *recorded != cfg {
			return knowledge.ErrIncompatible
		}

		// The source identifies the current document. Cascading deletion also removes its vectors.
		// Deletion and replacement share the transaction, so readers see a complete version.
		if err := tx.Exec("DELETE FROM clinic_documents WHERE source = ?", doc.Source).Error; err != nil {
			return err
		}

		if err := tx.Exec(`
			INSERT INTO clinic_documents
			(id, source, content, provider, model, dimensions, chunk_count, topic, exam, procedure, doctor)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			doc.ID, doc.Source, doc.Content, cfg.Provider, cfg.Model, cfg.Dimensions, doc.ChunkCount,
			doc.Metadata.Topic, doc.Metadata.Exam, doc.Metadata.Procedure, doc.Metadata.Doctor,
		).Error; err != nil {
			return err
		}

		for _, chunk := range chunks {
			if err := cfg.ValidateVector(chunk.Vector); err != nil {
				return err
			}

			if err := tx.Exec(`
				INSERT INTO clinic_document_chunks
				(document_id, ordinal, content, start_line, end_line, dimensions, embedding, page)
				VALUES (?, ?, ?, ?, ?, ?, ?::public.vector, ?)`,
				doc.ID, chunk.Ordinal, chunk.Text, chunk.StartLine, chunk.EndLine,
				cfg.Dimensions, vectorLiteral(chunk.Vector), chunk.Page,
			).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *Store) CorpusConfig(ctx context.Context) (*knowledge.EmbeddingConfig, error) {
	return corpusConfig(s.db.WithContext(ctx))
}

func (s *Store) Scopes(ctx context.Context) ([]knowledge.Metadata, error) {
	var scopes []knowledge.Metadata

	err := s.db.WithContext(ctx).Table("clinic_documents").
		Distinct("topic", "exam", "procedure", "doctor").
		Order("topic, exam, procedure, doctor").Find(&scopes).Error

	return scopes, err
}

func corpusConfig(db *gorm.DB) (*knowledge.EmbeddingConfig, error) {
	var configs []knowledge.EmbeddingConfig

	if err := db.Table("clinic_documents").Distinct("provider", "model", "dimensions").
		Limit(2).Find(&configs).Error; err != nil {
		return nil, err
	}

	if len(configs) == 0 {
		return nil, nil
	}

	if len(configs) > 1 {
		return nil, knowledge.ErrIncompatible
	}

	return &configs[0], nil
}

func (s *Store) SearchCorpus(
	ctx context.Context, cfg knowledge.EmbeddingConfig, vector []float32, limit int, filter knowledge.ResolvedFilter,
) ([]knowledge.Result, error) {
	if err := cfg.ValidateVector(vector); err != nil {
		return nil, err
	}

	results := make([]knowledge.Result, 0)

	err := s.db.WithContext(ctx).Raw(`
		SELECT c.document_id, d.source, c.ordinal, c.content AS text, c.start_line, c.end_line,
		       d.topic, d.exam, d.procedure, d.doctor, c.page,
		       1 - (c.embedding OPERATOR(public.<=>) ?::public.vector) AS similarity
		FROM clinic_document_chunks c JOIN clinic_documents d ON d.id = c.document_id
		WHERE d.provider = ? AND d.model = ? AND d.dimensions = ?
		  AND (? = '' OR d.topic = ?) AND (? = '' OR d.exam = ?)
		  AND (? = '' OR d.procedure = ?) AND (? = '' OR d.doctor = ?)
		  AND (? OR (d.exam = '' AND d.procedure = '' AND d.doctor = ''))
		  AND NOT EXISTS (
		      SELECT 1 FROM clinic_documents incompatible
		      WHERE incompatible.provider <> ? OR incompatible.model <> ? OR incompatible.dimensions <> ?
		  )
		ORDER BY c.embedding OPERATOR(public.<=>) ?::public.vector, c.document_id, c.ordinal
		LIMIT ?`,
		vectorLiteral(vector), cfg.Provider, cfg.Model, cfg.Dimensions,
		filter.Topic, filter.Topic, filter.Exam, filter.Exam,
		filter.Procedure, filter.Procedure, filter.Doctor, filter.Doctor,
		!filter.GeneralOnly,
		cfg.Provider, cfg.Model, cfg.Dimensions, vectorLiteral(vector), limit,
	).Scan(&results).Error
	if err != nil {
		return nil, err
	}

	// The SQL guard enforces compatibility without reloading metadata on successful searches.
	// Resolve an empty result explicitly so an incompatible configuration cannot pass silently.
	if len(results) == 0 {
		recorded, err := s.CorpusConfig(ctx)
		if err != nil {
			return nil, err
		}

		if recorded != nil && *recorded != cfg {
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
