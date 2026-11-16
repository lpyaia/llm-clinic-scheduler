//go:build integration

package postgres

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

type controlledEmbeddings struct{}

func (controlledEmbeddings) Embed(
	_ context.Context, _ knowledge.EmbeddingConfig, texts []string,
) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		switch {
		case strings.Contains(text, "estacionamento"):
			vectors[i] = []float32{1, 0, 0}
		case strings.Contains(text, "ônibus"):
			vectors[i] = []float32{0.8, 0.6, 0}
		default:
			vectors[i] = []float32{0, 0, 1}
		}
	}

	return vectors, nil
}

type embeddingFunc func(context.Context, knowledge.EmbeddingConfig, []string) ([][]float32, error)

func (fn embeddingFunc) Embed(
	ctx context.Context, cfg knowledge.EmbeddingConfig, texts []string,
) ([][]float32, error) {
	return fn(ctx, cfg, texts)
}

type unreadableDocument struct{}

func (unreadableDocument) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestKnowledgeRejectsUnreadableAndEmptyDocuments(t *testing.T) {
	f := setup(t)
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}
	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, source string
		input        io.Reader
		want         error
	}{
		{"empty", "faq.md", strings.NewReader(" \n\t"), knowledge.ErrEmpty},
		{"read failure", "faq.txt", unreadableDocument{}, knowledge.ErrExtraction},
		{"unsupported format", "faq.pdf", strings.NewReader("%PDF"), knowledge.ErrExtraction},
		{"invalid UTF-8", "faq.txt", strings.NewReader("\xff"), knowledge.ErrExtraction},
		{"binary", "faq.txt", strings.NewReader("hello\x00"), knowledge.ErrExtraction},
		{"oversized", "faq.md", strings.NewReader(strings.Repeat("a", 65537)), knowledge.ErrExtraction},
		{"too many paragraphs", "faq.md", strings.NewReader(strings.Repeat("a\n\n", 257)), knowledge.ErrExtraction},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc, err := service.Ingest(context.Background(), test.source, test.input)
			if !errors.Is(err, test.want) || doc.ID != uuid.Nil {
				t.Fatalf("document=%+v, error=%v; want %v", doc, err, test.want)
			}
		})
	}
}

func TestKnowledgeRejectsInvalidEmbeddings(t *testing.T) {
	f := setup(t)
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}
	working, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	existing, err := working.Ingest(context.Background(), "existing.txt", strings.NewReader("estacionamento"))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		vectors [][]float32
		err     error
	}{
		{name: "provider failure", err: errors.New("provider unavailable")},
		{name: "missing vector"},
		{name: "wrong dimension", vectors: [][]float32{{1, 0}}},
		{name: "zero vector", vectors: [][]float32{{0, 0, 0}}},
		{name: "NaN", vectors: [][]float32{{float32(math.NaN()), 0, 0}}},
		{name: "infinity", vectors: [][]float32{{float32(math.Inf(1)), 0, 0}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			embedder := embeddingFunc(func(context.Context, knowledge.EmbeddingConfig, []string) ([][]float32, error) {
				return test.vectors, test.err
			})
			service, err := knowledge.New(f.store, embedder, cfg)
			if err != nil {
				t.Fatal(err)
			}

			doc, err := service.Ingest(context.Background(), "faq.txt", strings.NewReader("Simulated FAQ"))
			if !errors.Is(err, knowledge.ErrEmbeddings) || doc.ID != uuid.Nil {
				t.Fatalf("document=%+v, error=%v", doc, err)
			}

			if _, err := service.Search(
				context.Background(),
				existing.ID,
				"parking",
				1,
			); !errors.Is(
				err,
				knowledge.ErrEmbeddings,
			) {
				t.Fatalf("search must reject invalid query embeddings: %v", err)
			}
		})
	}

	results, err := working.Search(context.Background(), existing.ID, "estacionamento", 1)
	if err != nil || len(results) != 1 || results[0].Text != "estacionamento" {
		t.Fatalf("embedding failures changed the existing document: %+v, %v", results, err)
	}
}

func TestKnowledgeRejectsIncompatibleSearchAndConfiguration(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}
	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := service.Ingest(ctx, "faq.txt", strings.NewReader("O estacionamento é gratuito."))
	if err != nil {
		t.Fatal(err)
	}

	for _, changed := range []knowledge.EmbeddingConfig{
		{Provider: "other", Model: "faq-v1", Dimensions: 3},
		{Provider: "controlled", Model: "faq-v2", Dimensions: 3},
		{Provider: "controlled", Model: "faq-v1", Dimensions: 2},
	} {
		other, err := knowledge.New(f.store, controlledEmbeddings{}, changed)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := other.Search(ctx, doc.ID, "estacionamento", 3); !errors.Is(err, knowledge.ErrIncompatible) {
			t.Fatalf("configuration %+v: %v", changed, err)
		}
	}

	for _, invalid := range []knowledge.EmbeddingConfig{
		{}, {Provider: "controlled", Model: "faq-v1", Dimensions: 0},
		{Provider: "controlled", Model: "faq-v1", Dimensions: 16001},
		{Provider: "controlled", Model: " ", Dimensions: 3},
	} {
		if _, err := knowledge.New(
			f.store,
			controlledEmbeddings{},
			invalid,
		); !errors.Is(
			err,
			knowledge.ErrConfiguration,
		) {
			t.Fatalf("accepted configuration: %+v, error=%v", invalid, err)
		}
	}

	if _, err := service.Search(ctx, uuid.New(), "estacionamento", 1); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatalf("missing document error=%v", err)
	}

	if _, err := service.Search(ctx, doc.ID, " ", 1); !errors.Is(err, knowledge.ErrInput) {
		t.Fatalf("empty question error=%v", err)
	}
}

func TestKnowledgePreservesLongUnicodeLinesAndCRLFCitations(t *testing.T) {
	f := setup(t)
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}
	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	content := "\ufeff\r\n" + strings.Repeat("á", 1005) + "\r\nfinal\r\n"
	doc, err := service.Ingest(context.Background(), "faq.txt", strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}

	results, err := service.Search(context.Background(), doc.ID, "horários", 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 || results[0].Text != strings.Repeat("á", 1000) ||
		results[1].Text != "ááááá\nfinal" || results[0].StartLine != 2 || results[0].EndLine != 2 ||
		results[1].StartLine != 2 || results[1].EndLine != 3 {
		t.Fatalf("lost text or citation location: %+v", results)
	}
}

func TestKnowledgeHandlesWhitespaceAtChunkBoundary(t *testing.T) {
	f := setup(t)
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}
	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := service.Ingest(context.Background(), "faq.txt",
		strings.NewReader(strings.Repeat(" ", 1000)+"FAQ"))
	if err != nil {
		t.Fatal(err)
	}

	results, err := service.Search(context.Background(), doc.ID, "FAQ", 10)
	if err != nil || len(results) != 1 || results[0].Text != "FAQ" || results[0].StartLine != 1 {
		t.Fatalf("unexpected whitespace handling: %+v, %v", results, err)
	}
}

func TestKnowledgeCancelledPersistencePreservesExistingDocument(t *testing.T) {
	f := setup(t)
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}
	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := service.Ingest(context.Background(), "existing.txt", strings.NewReader("estacionamento"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	embedder := embeddingFunc(func(
		ctx context.Context, cfg knowledge.EmbeddingConfig, texts []string,
	) ([][]float32, error) {
		vectors, err := (controlledEmbeddings{}).Embed(ctx, cfg, texts)
		cancel()

		return vectors, err
	})
	cancelled, err := knowledge.New(f.store, embedder, cfg)
	if err != nil {
		t.Fatal(err)
	}

	failed, err := cancelled.Ingest(ctx, "cancelled.txt", strings.NewReader("first\n\nsecond"))
	if !errors.Is(err, context.Canceled) || failed.ID != uuid.Nil {
		t.Fatalf("cancelled write returned document=%+v, error=%v", failed, err)
	}

	results, err := service.Search(context.Background(), doc.ID, "estacionamento", 1)
	if err != nil || len(results) != 1 || results[0].Text != "estacionamento" {
		t.Fatalf("cancelled write changed the existing document: %+v, %v", results, err)
	}
}

func TestKnowledgeIngestionAndRankedCitations(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}
	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	const faq = "# FAQ simulada\n\nO estacionamento é gratuito.\n\nHá ônibus na porta.\n\nAtendemos das 8h às 18h.\n"
	doc, err := service.Ingest(ctx, "faq.md", strings.NewReader(faq))
	if err != nil {
		t.Fatal(err)
	}

	// A fresh service reads the persisted document and vectors after ingestion.
	reader, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	results, err := reader.Search(ctx, doc.ID, "Tem estacionamento?", 3)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 3 || results[0].Text != "O estacionamento é gratuito." ||
		results[1].Text != "Há ônibus na porta." || results[0].Similarity < 0.999 ||
		results[1].Similarity < 0.799 || results[1].Similarity > 0.801 ||
		results[0].DocumentID != doc.ID || results[0].Source != "faq.md" ||
		results[0].StartLine != 3 || results[0].EndLine != 3 {
		t.Fatalf("unexpected ranked citations: %+v", results)
	}
}
