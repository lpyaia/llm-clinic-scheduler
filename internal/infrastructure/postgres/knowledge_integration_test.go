//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"

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

func TestKnowledgeReingestionReplacesCurrentChunks(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	old, err := service.Ingest(ctx, "politica.md", strings.NewReader("estacionamento antigo\n\nônibus removido"))
	if err != nil {
		t.Fatal(err)
	}

	current, err := service.Ingest(ctx, "politica.md", strings.NewReader("Atendimento atualizado."))
	if err != nil {
		t.Fatal(err)
	}

	rows, err := service.Search(ctx, "estacionamento", 10)
	if err != nil || len(rows) != 1 || rows[0].Text != "Atendimento atualizado." ||
		rows[0].DocumentID != current.ID || current.ID == old.ID {
		t.Fatalf("reingestion must replace the old document and all its chunks: %+v, %v", rows, err)
	}
}

func TestKnowledgeFiltersBeforeRanking(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, doc := range []struct {
		source, text string
		metadata     knowledge.Metadata
	}{
		{"abdome.md", "Orientação administrativa para ultrassom abdominal.",
			knowledge.Metadata{Topic: "preparation", Exam: "Ultrassom abdominal"}},
		{"tireoide.md", "estacionamento: orientação de ultrassom da tireoide.",
			knowledge.Metadata{Topic: "preparation", Exam: "Ultrassom da tireoide"}},
		{"politica.md", "estacionamento: política de ultrassom abdominal.",
			knowledge.Metadata{Topic: "policy", Exam: "Ultrassom abdominal"}},
	} {
		if _, err := service.Ingest(ctx, doc.source, strings.NewReader(doc.text), doc.metadata); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := service.Search(ctx, "estacionamento", 1,
		knowledge.Filter{Topic: "preparation", Exam: "ULTRASSOM ABDOMINAL"})
	if err != nil || len(rows) != 1 || rows[0].Source != "abdome.md" ||
		rows[0].Metadata.Exam != "Ultrassom abdominal" {
		t.Fatalf("scope must apply before ranking and limit: %+v, %v", rows, err)
	}

	_, err = service.Search(ctx, "preparação do ultrassom", 3,
		knowledge.Filter{Topic: "preparation", Exam: "ultrassom"})
	if !errors.Is(err, knowledge.ErrAmbiguous) {
		t.Fatalf("ambiguous entity must not retrieve either preparation: %v", err)
	}
}

func TestKnowledgePDFPreservesPageLocations(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	const source = "../../../docs/knowledge/preparo-ultrassom-abdominal-simulated.pdf"

	file, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	_, err = service.Ingest(ctx, source, file,
		knowledge.Metadata{Topic: "preparation", Exam: "Ultrassom abdominal"})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := service.Search(ctx, "formulário azul", 100,
		knowledge.Filter{Topic: "preparation", Exam: "Ultrassom abdominal"})
	if err != nil {
		t.Fatal(err)
	}

	found := false

	for _, row := range rows {
		if strings.Contains(row.Text, "sala de apoio 2") {
			found = true

			if row.Source != source || row.Page != 2 || row.StartLine < 1 || row.EndLine < row.StartLine {
				t.Fatalf("lost PDF page or extracted line location: %+v", row)
			}
		}
	}

	if !found {
		t.Fatalf("second PDF page missing from retrieval: %+v", rows)
	}
}

func TestKnowledgeFiltersProcedureAndDoctorAndAsksForMissingContext(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, doc := range []struct {
		source string
		meta   knowledge.Metadata
	}{
		{"ana-souza.md", knowledge.Metadata{Topic: "doctor_profile", Doctor: "Dra. Ana Souza"}},
		{"ana-lima.md", knowledge.Metadata{Topic: "doctor_profile", Doctor: "Dra. Ana Lima"}},
		{"curativo.md", knowledge.Metadata{Topic: "policy", Procedure: "Curativo simples"}},
		{"biopsia.md", knowledge.Metadata{Topic: "policy", Procedure: "Biópsia de pele"}},
	} {
		if _, err := service.Ingest(ctx, doc.source, strings.NewReader(doc.source), doc.meta); err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range []struct {
		filter knowledge.Filter
		source string
	}{
		{knowledge.Filter{Topic: "doctor_profile", Doctor: "ana souza"}, "ana-souza.md"},
		{knowledge.Filter{Topic: "policy", Procedure: "BIOPSIA DE PELE"}, "biopsia.md"},
	} {
		rows, err := service.Search(ctx, "informações", 10, test.filter)
		if err != nil || len(rows) != 1 || rows[0].Source != test.source {
			t.Fatalf("wrong entity scope for %+v: %+v, %v", test.filter, rows, err)
		}
	}

	for _, filter := range []knowledge.Filter{
		{Topic: "doctor_profile", Doctor: "Ana"}, {Topic: "doctor_profile"}, {Topic: "policy"},
	} {
		if _, err := service.Search(ctx, "informações", 10, filter); !errors.Is(err, knowledge.ErrAmbiguous) {
			t.Fatalf("missing or ambiguous context must ask instead of mixing entities: %+v, %v", filter, err)
		}
	}

	rows, err := service.Search(ctx, "informações", 10, knowledge.Filter{Topic: "policy", Procedure: "inexistente"})
	if err != nil || len(rows) != 0 {
		t.Fatalf("unknown entities must not fall back to another context: %+v, %v", rows, err)
	}
}

func TestKnowledgeGeneralPolicyDoesNotRetrieveEntityPolicies(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Ingest(ctx, "geral.md", strings.NewReader("Política geral."),
		knowledge.Metadata{Topic: "policy"}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Ingest(ctx, "exame.md", strings.NewReader("estacionamento: política específica."),
		knowledge.Metadata{Topic: "policy", Exam: "Ultrassom abdominal"}); err != nil {
		t.Fatal(err)
	}

	rows, err := service.Search(ctx, "estacionamento", 10, knowledge.Filter{Topic: "policy"})
	if err != nil || len(rows) != 1 || rows[0].Source != "geral.md" {
		t.Fatalf("general policies must exclude exam-specific contexts: %+v, %v", rows, err)
	}
}

func TestKnowledgePreparationAsksForDoctorWhenInstructionsDependOnDoctor(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, doctor := range []string{"Ana Souza", "Ana Lima"} {
		if _, err := service.Ingest(ctx, doctor+".md", strings.NewReader("Orientação de "+doctor),
			knowledge.Metadata{Topic: "preparation", Exam: "Ultrassom abdominal", Doctor: doctor}); err != nil {
			t.Fatal(err)
		}
	}

	_, err = service.Search(ctx, "Como preparar?", 3,
		knowledge.Filter{Topic: "preparation", Exam: "Ultrassom abdominal"})
	if !errors.Is(err, knowledge.ErrAmbiguous) {
		t.Fatalf("an exact exam must not mix preparations belonging to different doctors: %v", err)
	}
}

func TestKnowledgeFailedReplacementPreservesPreviousVersion(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	old, err := service.Ingest(ctx, "current.md", strings.NewReader("estacionamento anterior"),
		knowledge.Metadata{Topic: "policy", Exam: "Exame anterior"})
	if err != nil {
		t.Fatal(err)
	}

	f.must(f.store.db.Exec(`
		CREATE FUNCTION reject_replacement() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
		    IF NEW.ordinal = 2 THEN RAISE EXCEPTION 'simulated failure'; END IF;
		    RETURN NEW;
		END;
		$$
	`).Error)
	f.must(f.store.db.Exec(`
		CREATE TRIGGER reject_replacement BEFORE INSERT ON clinic_document_chunks
		FOR EACH ROW EXECUTE FUNCTION reject_replacement()
	`).Error)

	_, err = service.Ingest(ctx, "current.md", strings.NewReader("novo\n\nsegundo trecho"),
		knowledge.Metadata{Topic: "preparation", Exam: "Exame novo"})
	if err == nil {
		t.Fatal("expected a failed replacement")
	}

	rows, err := service.Search(ctx, "estacionamento", 10,
		knowledge.Filter{Topic: "policy", Exam: "Exame anterior"})
	if err != nil || len(rows) != 1 || rows[0].DocumentID != old.ID || rows[0].Text != "estacionamento anterior" {
		t.Fatalf("failed replacement lost previous metadata, text or vector: %+v, %v", rows, err)
	}

	f.must(f.store.db.Exec("DROP TRIGGER reject_replacement ON clinic_document_chunks").Error)

	for range 2 {
		if _, err := service.Ingest(ctx, "current.md", strings.NewReader("atual"),
			knowledge.Metadata{Topic: "preparation", Exam: "Exame novo"}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err = service.Search(ctx, "estacionamento", 10)
	if err != nil || len(rows) != 1 || rows[0].Text != "atual" || rows[0].Metadata.Exam != "Exame novo" {
		t.Fatalf("repeated replacement kept stale data or duplicate vectors: %+v, %v", rows, err)
	}

	rows, err = service.Search(ctx, "estacionamento", 10,
		knowledge.Filter{Topic: "policy", Exam: "Exame anterior"})
	if err != nil || len(rows) != 0 {
		t.Fatalf("removed scope remains searchable: %+v, %v", rows, err)
	}
}

func TestKnowledgeSimulatedCorpusCanBeIngestedWithItsMetadata(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile("../../../docs/knowledge/corpus.json")
	if err != nil {
		t.Fatal(err)
	}

	var entries []struct {
		File string `json:"file"`
		knowledge.Metadata
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		file, err := os.Open("../../../" + entry.File)
		if err != nil {
			t.Fatal(err)
		}

		_, err = service.Ingest(ctx, entry.File, file, entry.Metadata)
		_ = file.Close()

		if err != nil {
			t.Fatalf("ingest %s: %v", entry.File, err)
		}
	}

	for _, topic := range []string{"general", "faq", "policy", "preparation", "insurance", "doctor_profile"} {
		filter := knowledge.Filter{Topic: topic}
		if topic == "preparation" {
			filter.Exam = "Ultrassom abdominal"
		}

		if topic == "doctor_profile" {
			filter.Doctor = "Dra. Ana Souza"
		}

		rows, err := service.Search(ctx, "materiais", 100, filter)
		if err != nil || len(rows) == 0 {
			t.Fatalf("corpus has no retrievable %s material: %+v, %v", topic, rows, err)
		}

		for _, row := range rows {
			if row.Metadata.Topic != topic {
				t.Fatalf("topic leakage: %+v", row)
			}
		}
	}
}

func TestKnowledgeSearchRanksAcrossDocuments(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	parking, err := service.Ingest(ctx, "acesso.md", strings.NewReader("\nO estacionamento é gratuito."))
	if err != nil {
		t.Fatal(err)
	}

	bus, err := service.Ingest(ctx, "transporte.md", strings.NewReader("Há ônibus na porta."))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Ingest(ctx, "horarios.md", strings.NewReader("Atendemos das 8h às 18h.")); err != nil {
		t.Fatal(err)
	}

	results, err := service.Search(ctx, "Tem estacionamento?", 2)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 || results[0].DocumentID != parking.ID || results[1].DocumentID != bus.ID ||
		results[0].Text != "O estacionamento é gratuito." || results[1].Text != "Há ônibus na porta." ||
		results[0].Source != "acesso.md" || results[1].Source != "transporte.md" ||
		results[0].StartLine != 2 || results[0].EndLine != 2 || results[1].StartLine != 1 ||
		results[0].Similarity < 0.999 || results[1].Similarity < 0.799 || results[1].Similarity > 0.801 {
		t.Fatalf("unexpected corpus ranking or citations: %+v", results)
	}
}

func TestKnowledgeEmptyCorpusReturnsNoResults(t *testing.T) {
	f := setup(t)
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}
	embedder := embeddingFunc(func(context.Context, knowledge.EmbeddingConfig, []string) ([][]float32, error) {
		return nil, errors.New("an empty corpus must not request embeddings")
	})

	service, err := knowledge.New(f.store, embedder, cfg)
	if err != nil {
		t.Fatal(err)
	}

	results, err := service.Search(context.Background(), "Tem estacionamento?", 3)
	if err != nil || results == nil || len(results) != 0 {
		t.Fatalf("empty corpus results=%+v, error=%v", results, err)
	}
}

func TestKnowledgeRejectsLegacyMixedCorpus(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Ingest(ctx, "acesso.md", strings.NewReader("estacionamento")); err != nil {
		t.Fatal(err)
	}

	other, err := service.Ingest(ctx, "transporte.md", strings.NewReader("ônibus"))
	if err != nil {
		t.Fatal(err)
	}

	// Simulate documents imported by the previous version, which permitted separate configurations.
	f.must(f.store.db.Exec("UPDATE clinic_documents SET model = 'legacy-model' WHERE id = ?", other.ID).Error)

	if _, err := service.Search(ctx, "estacionamento", 3); !errors.Is(err, knowledge.ErrIncompatible) {
		t.Fatalf("mixed corpus must fail instead of silently searching a compatible subset: %v", err)
	}

	if _, err := service.Ingest(
		ctx,
		"new.md",
		strings.NewReader("new text"),
	); !errors.Is(
		err,
		knowledge.ErrIncompatible,
	) {
		t.Fatalf("mixed corpus must reject further imports: %v", err)
	}
}

func TestKnowledgeConcurrentFirstImportsKeepOneConfiguration(t *testing.T) {
	f := setup(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	embedder := embeddingFunc(func(
		ctx context.Context, cfg knowledge.EmbeddingConfig, texts []string,
	) ([][]float32, error) {
		ready <- struct{}{}

		select {
		case <-release:
			return (controlledEmbeddings{}).Embed(ctx, cfg, texts)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	type outcome struct {
		doc knowledge.Document
		err error
	}

	completed := make(chan outcome, 2)

	for _, model := range []string{"faq-v1", "faq-v2"} {
		cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: model, Dimensions: 3}

		service, err := knowledge.New(f.store, embedder, cfg)
		if err != nil {
			t.Fatal(err)
		}

		go func() {
			doc, err := service.Ingest(ctx, model+".md", strings.NewReader("estacionamento"))
			completed <- outcome{doc: doc, err: err}
		}()
	}

	// Hold both external responses until each import has observed the empty corpus.
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("imports did not reach the embedding provider before the deadline")
		}
	}

	close(release)

	var (
		winner                   knowledge.Document
		successful, incompatible int
	)

	for range 2 {
		result := <-completed
		switch {
		case result.err == nil:
			winner = result.doc
			successful++
		case errors.Is(result.err, knowledge.ErrIncompatible):
			incompatible++
		default:
			t.Fatalf("unexpected concurrent import failure: %v", result.err)
		}
	}

	if successful != 1 || incompatible != 1 {
		t.Fatalf("successful=%d, incompatible=%d; expected one of each", successful, incompatible)
	}

	reader, err := knowledge.New(f.store, controlledEmbeddings{}, winner.EmbeddingConfig)
	if err != nil {
		t.Fatal(err)
	}

	results, err := reader.Search(ctx, "estacionamento", 10)
	if err != nil || len(results) != 1 || results[0].DocumentID != winner.ID {
		t.Fatalf("concurrent imports left an inconsistent corpus: %+v, %v", results, err)
	}
}

func TestKnowledgeChunkWriteFailureRollsBackWholeDocument(t *testing.T) {
	f := setup(t)
	cfg := knowledge.EmbeddingConfig{Provider: "controlled", Model: "faq-v1", Dimensions: 3}

	service, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Inject a database failure after the document and first chunk have been inserted.
	f.must(f.store.db.Exec(`
		CREATE FUNCTION reject_second_chunk() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
		    IF NEW.ordinal = 2 THEN
		        RAISE EXCEPTION 'simulated chunk write failure';
		    END IF;
		    RETURN NEW;
		END;
		$$
	`).Error)
	f.must(f.store.db.Exec(`
		CREATE TRIGGER reject_second_chunk BEFORE INSERT ON clinic_document_chunks
		FOR EACH ROW EXECUTE FUNCTION reject_second_chunk()
	`).Error)

	doc, err := service.Ingest(context.Background(), "failed.md", strings.NewReader("first\n\nsecond"))
	if err == nil || doc.ID != uuid.Nil {
		t.Fatalf("expected failed import: document=%+v, error=%v", doc, err)
	}

	results, err := service.Search(context.Background(), "first", 10)
	if err != nil || len(results) != 0 {
		t.Fatalf("failed transaction left a document or partial chunks: %+v, %v", results, err)
	}

	// A different model can establish the still-empty corpus after the failed transaction.
	f.must(f.store.db.Exec("DROP TRIGGER reject_second_chunk ON clinic_document_chunks").Error)

	cfg.Model = "faq-v2"

	retry, err := knowledge.New(f.store, controlledEmbeddings{}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := retry.Ingest(context.Background(), "retry.md", strings.NewReader("estacionamento")); err != nil {
		t.Fatalf("rolled-back import retained its configuration: %v", err)
	}
}

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

	_, err = working.Ingest(context.Background(), "existing.txt", strings.NewReader("estacionamento"))
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

	results, err := working.Search(context.Background(), "estacionamento", 1)
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

	_, err = service.Ingest(ctx, "faq.txt", strings.NewReader("O estacionamento é gratuito."))
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

		if _, err := other.Search(ctx, "estacionamento", 3); !errors.Is(err, knowledge.ErrIncompatible) {
			t.Fatalf("configuration %+v: %v", changed, err)
		}

		doc, err := other.Ingest(ctx, "incompatible.txt", strings.NewReader("new document"))
		if !errors.Is(err, knowledge.ErrIncompatible) || doc.ID != uuid.Nil {
			t.Fatalf("incompatible import: document=%+v, error=%v", doc, err)
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

	if _, err := service.Search(ctx, " ", 1); !errors.Is(err, knowledge.ErrInput) {
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

	_, err = service.Ingest(context.Background(), "faq.txt", strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}

	results, err := service.Search(context.Background(), "horários", 10)
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

	_, err = service.Ingest(context.Background(), "faq.txt",
		strings.NewReader(strings.Repeat(" ", 1000)+"FAQ"))
	if err != nil {
		t.Fatal(err)
	}

	results, err := service.Search(context.Background(), "FAQ", 10)
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

	_, err = service.Ingest(context.Background(), "existing.txt", strings.NewReader("estacionamento"))
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

	results, err := service.Search(context.Background(), "estacionamento", 1)
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

	results, err := reader.Search(ctx, "Tem estacionamento?", 3)
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
