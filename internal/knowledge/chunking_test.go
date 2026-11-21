package knowledge_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

type chunkRepository struct {
	knowledge.Repository
	config *knowledge.EmbeddingConfig
	rows   []knowledge.Result
}

func (r *chunkRepository) CorpusConfig(context.Context) (*knowledge.EmbeddingConfig, error) {
	return r.config, nil
}

func (r *chunkRepository) SaveDocument(_ context.Context, doc knowledge.Document, chunks []knowledge.Chunk) error {
	r.config = &doc.EmbeddingConfig
	r.rows = nil
	for _, chunk := range chunks {
		r.rows = append(r.rows, knowledge.Result{
			DocumentID: doc.ID, Source: doc.Source, Ordinal: chunk.Ordinal, Text: chunk.Text,
			StartLine: chunk.StartLine, EndLine: chunk.EndLine,
		})
	}

	return nil
}

func (r *chunkRepository) SearchCorpus(
	context.Context, knowledge.EmbeddingConfig, []float32, int, knowledge.ResolvedFilter,
) ([]knowledge.Result, error) {
	return r.rows, nil
}

type unitEmbedder struct{}

func (unitEmbedder) Embed(_ context.Context, _ knowledge.EmbeddingConfig, texts []string) ([][]float32, error) {
	rows := make([][]float32, len(texts))
	for index := range rows {
		rows[index] = []float32{1}
	}

	return rows, nil
}

func TestIngestSearchUsesConfiguredChunkingWithoutChangingDefault(t *testing.T) {
	for _, test := range []struct {
		name    string
		options []knowledge.Chunking
		want    []string
	}{
		{"default paragraphs", nil, []string{"ab", "cd"}},
		{"overlapping windows", []knowledge.Chunking{{Strategy: "windows", MaxRunes: 4, Overlap: 1}},
			[]string{"ab\n\n", "\ncd"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &chunkRepository{}

			service, err := knowledge.New(repository, unitEmbedder{}, knowledge.EmbeddingConfig{
				Provider: "fixture", Model: "unit", Dimensions: 1,
			}, test.options...)
			if err != nil {
				t.Fatal(err)
			}

			_, err = service.Ingest(context.Background(), "access.md", strings.NewReader("ab\n\ncd"))
			if err != nil {
				t.Fatal(err)
			}

			rows, err := service.Search(context.Background(), "access", 100)
			if err != nil || len(rows) != len(test.want) {
				t.Fatalf("unexpected chunks: %+v, %v", rows, err)
			}

			for index, text := range test.want {
				if rows[index].Text != text {
					t.Fatalf("chunk %d: want %q, got %q", index+1, text, rows[index].Text)
				}
			}
		})
	}
}
