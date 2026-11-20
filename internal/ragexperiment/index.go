package ragexperiment

import (
	"context"
	"math"
	"slices"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

type indexedDocument struct {
	document knowledge.Document
	chunks   []knowledge.Chunk
}

// vectorIndex is owned by one matrix cell and discarded when that cell finishes.
// It performs exact cosine search; source/ordinal ties are stable across rebuilds.
type vectorIndex struct {
	documents map[string]indexedDocument
	config    *knowledge.EmbeddingConfig
}

func (v *vectorIndex) SaveDocument(ctx context.Context, doc knowledge.Document, chunks []knowledge.Chunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if v.config != nil && *v.config != doc.EmbeddingConfig {
		return knowledge.ErrIncompatible
	}

	for _, chunk := range chunks {
		if err := doc.EmbeddingConfig.ValidateVector(chunk.Vector); err != nil {
			return err
		}
	}

	v.config = &doc.EmbeddingConfig
	v.documents[doc.Source] = indexedDocument{document: doc, chunks: chunks}

	return nil
}

func (v *vectorIndex) CorpusConfig(ctx context.Context) (*knowledge.EmbeddingConfig, error) {
	return v.config, ctx.Err()
}

func (v *vectorIndex) Scopes(ctx context.Context) ([]knowledge.Metadata, error) {
	var scopes []knowledge.Metadata

	seen := make(map[knowledge.Metadata]bool)

	for _, document := range v.documents {
		metadata := document.document.Metadata
		if !seen[metadata] {
			scopes = append(scopes, metadata)
		}

		seen[metadata] = true
	}

	return scopes, ctx.Err()
}

func (v *vectorIndex) SearchCorpus(
	ctx context.Context, config knowledge.EmbeddingConfig, vector []float32, limit int, filter knowledge.ResolvedFilter,
) ([]knowledge.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if v.config == nil || *v.config != config {
		return nil, knowledge.ErrIncompatible
	}

	if err := config.ValidateVector(vector); err != nil {
		return nil, err
	}

	rows := []knowledge.Result{}

	for _, document := range v.documents {
		meta := document.document.Metadata
		if (filter.Topic != "" && filter.Topic != meta.Topic) || (filter.Exam != "" && filter.Exam != meta.Exam) ||
			(filter.Procedure != "" && filter.Procedure != meta.Procedure) ||
			(filter.Doctor != "" && filter.Doctor != meta.Doctor) ||
			(filter.GeneralOnly && (meta.Exam != "" || meta.Procedure != "" || meta.Doctor != "")) {
			continue
		}

		for _, chunk := range document.chunks {
			row := documentResult(document.document, chunk)
			row.Similarity = cosine(vector, chunk.Vector)
			rows = append(rows, row)
		}
	}

	slices.SortFunc(rows, func(a, b knowledge.Result) int {
		if a.Similarity > b.Similarity {
			return -1
		}

		if a.Similarity < b.Similarity {
			return 1
		}

		if a.Source < b.Source {
			return -1
		}

		if a.Source > b.Source {
			return 1
		}

		return a.Ordinal - b.Ordinal
	})

	return rows[:min(limit, len(rows))], nil
}

func cosine(a, b []float32) float64 {
	var dot, left, right float64

	for index, value := range a {
		x, y := float64(value), float64(b[index])
		dot, left, right = dot+x*y, left+x*x, right+y*y
	}

	return dot / math.Sqrt(left*right)
}

func documentResult(doc knowledge.Document, chunk knowledge.Chunk) knowledge.Result {
	return knowledge.Result{
		DocumentID: doc.ID, Source: doc.Source, Ordinal: chunk.Ordinal, Text: chunk.Text,
		StartLine: chunk.StartLine, EndLine: chunk.EndLine, Metadata: doc.Metadata, Page: chunk.Page,
	}
}

func normalizeRow(row knowledge.Result, indexID string) knowledge.Result {
	row.DocumentID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(indexID+"\x00"+row.Source))

	return row
}

func (v *vectorIndex) snapshot(indexID string) []Passage {
	rows := []Passage{}

	for _, document := range v.documents {
		for _, chunk := range document.chunks {
			rows = append(rows, Passage{
				Result:    normalizeRow(documentResult(document.document, chunk), indexID),
				StartRune: chunk.StartRune, EndRune: chunk.EndRune,
			})
		}
	}

	slices.SortFunc(rows, func(a, b Passage) int {
		if a.Source < b.Source {
			return -1
		}

		if a.Source > b.Source {
			return 1
		}

		return a.Ordinal - b.Ordinal
	})

	return rows
}
