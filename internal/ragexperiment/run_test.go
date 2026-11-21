package ragexperiment_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/ragexperiment"
)

type unitEmbeddings struct{}

func (unitEmbeddings) Embed(_ context.Context, _ knowledge.EmbeddingConfig, texts []string) ([][]float32, error) {
	rows := make([][]float32, len(texts))
	for index := range texts {
		rows[index] = []float32{1}
	}

	return rows, nil
}

type selectAll struct{}

func (selectAll) Complete(_ context.Context, request agent.Request) (agent.Response, error) {
	var excerpts []struct {
		ID string `json:"excerpt_id"`
	}
	if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &excerpts); err != nil {
		return agent.Response{}, err
	}

	ids := []string{}
	for _, excerpt := range excerpts {
		ids = append(ids, excerpt.ID)
	}

	content, err := json.Marshal(map[string]any{"excerpt_ids": ids})

	return agent.Response{Content: content}, err
}

func tinyDataset() ragexperiment.Dataset {
	return ragexperiment.Dataset{
		Version: 1, CorpusID: "two-facts",
		Documents: []ragexperiment.Document{{Source: "faq.md", Lines: []string{"parking", "", "ramp"}}},
		Questions: []ragexperiment.Question{{
			ID: "access", Text: "parking ramp", Reference: "Parking and ramp.",
			Expected: []ragexperiment.Evidence{{Source: "faq.md", Text: "parking"}, {Source: "faq.md", Text: "ramp"}},
		}},
	}
}

func tinyMatrix() ragexperiment.Matrix {
	return ragexperiment.Matrix{
		Chunking: []knowledge.Chunking{
			{Strategy: "paragraphs", MaxRunes: 20}, {Strategy: "windows", MaxRunes: 5},
		},
		TopK: []int{1, 3},
	}
}

func fixtureOptions() ragexperiment.Options {
	return ragexperiment.Options{
		Embedder: unitEmbeddings{}, Model: selectAll{},
		Settings: ragexperiment.Settings{
			Mode: "fixture", Embedding: knowledge.EmbeddingConfig{Provider: "fixture", Model: "unit", Dimensions: 1},
			Generation: ragexperiment.Generation{Provider: "fixture", Model: "select-all"},
		},
	}
}

func TestMatrixKeepsReferenceFactsStableAcrossFreshChunkIndexes(t *testing.T) {
	report, err := ragexperiment.Run(context.Background(), tinyDataset(), tinyMatrix(), fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Cells) != 4 {
		t.Fatalf("expected 2 x 2 matrix: %+v", report)
	}

	seen := make(map[string]bool)

	for index, want := range []float64{0.5, 1, 0, 1} {
		cell := report.Cells[index]
		if cell.Metrics.Recall.Value == nil || *cell.Metrics.Recall.Value != want ||
			cell.Metrics.Recall.Count != 1 || seen[cell.IndexID] {
			t.Fatalf("cell %d: want recall %v and an isolated index: %+v", index, want, cell)
		}

		seen[cell.IndexID] = true

		if cell.Scenarios[0].Question.ID != "access" || len(cell.Scenarios[0].Question.Expected) != 2 ||
			cell.Scenarios[0].Assessment.Groundedness == nil || *cell.Scenarios[0].Assessment.Groundedness != 1 {
			t.Fatalf("questions, labels and evidence scoring must remain inspectable: %+v", cell)
		}
	}

	if !*report.Cells[1].Scenarios[0].AnswerCorrect || !*report.Cells[3].Scenarios[0].AnswerCorrect ||
		*report.Cells[0].Scenarios[0].AnswerCorrect || *report.Cells[2].Scenarios[0].AnswerCorrect {
		t.Fatal("an answer must cover all reference facts, including facts divided across chunk boundaries")
	}
}

type failingModel struct{}

func (failingModel) Complete(context.Context, agent.Request) (agent.Response, error) {
	return agent.Response{}, errors.New("provider unavailable")
}

func TestGenerationErrorsNeverApproveOrInflateQualityMetrics(t *testing.T) {
	options := fixtureOptions()
	options.Model = failingModel{}

	report, err := ragexperiment.Run(context.Background(), tinyDataset(), tinyMatrix(), options)
	if err != nil {
		t.Fatal(err)
	}

	for _, cell := range report.Cells {
		if cell.Metrics.Errors != 1 || cell.Metrics.Passed != 0 || cell.Metrics.AnswerCorrect.Value != nil ||
			cell.Metrics.Groundedness.Value != nil || cell.Metrics.Recall.Value == nil ||
			cell.Scenarios[0].Error == "" {
			t.Fatalf("generation failure must retain retrieval evidence without inventing quality scores: %+v", cell)
		}
	}
}

func TestEmptyRetrievalAbstainsAndStaysOutsideGroundingAverages(t *testing.T) {
	dataset := tinyDataset()
	dataset.Questions = []ragexperiment.Question{{
		ID: "empty", Text: "unknown policy", Reference: "No evidence in this scope.", NoAnswer: true,
		Expected: []ragexperiment.Evidence{}, Filter: knowledge.Filter{Topic: "insurance"},
	}}
	options := fixtureOptions()
	options.Model = failingModel{}

	report, err := ragexperiment.Run(context.Background(), dataset, tinyMatrix(), options)
	if err != nil {
		t.Fatal(err)
	}

	for _, cell := range report.Cells {
		if cell.Metrics.Passed != 1 || cell.Metrics.Recall.Value != nil ||
			cell.Metrics.Groundedness.Value != nil || cell.Metrics.CitationCorrectness.Value != nil ||
			*cell.Metrics.AbstentionCorrect.Value != 1 || cell.Scenarios[0].Request != nil {
			t.Fatalf("empty retrieval should abstain without consulting the model: %+v", cell)
		}
	}
}

func TestEvidenceOverlapDoesNotArtificiallyIncreaseRecall(t *testing.T) {
	matrix := tinyMatrix()
	matrix.Chunking[1] = knowledge.Chunking{Strategy: "windows", MaxRunes: 8, Overlap: 7}

	report, err := ragexperiment.Run(context.Background(), tinyDataset(), matrix, fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}

	if *report.Cells[3].Scenarios[0].Recall != 0.5 {
		t.Fatalf("three overlapping chunks containing only parking cannot cover ramp: %+v", report.Cells[3])
	}
}

func TestRecallUsesRuneOffsetsForRepeatedUnicodeText(t *testing.T) {
	dataset := tinyDataset()
	dataset.Documents[0].Lines = []string{"áááááááááá coração"}
	dataset.Questions[0].Expected = []ragexperiment.Evidence{{Source: "faq.md", Text: "coração"}}
	matrix := tinyMatrix()
	matrix.Chunking = []knowledge.Chunking{
		{Strategy: "paragraphs", MaxRunes: 8}, {Strategy: "windows", MaxRunes: 8, Overlap: 4},
	}

	report, err := ragexperiment.Run(context.Background(), dataset, matrix, fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}

	for index, want := range []float64{0, 1, 0, 0} {
		if *report.Cells[index].Scenarios[0].Recall != want {
			t.Fatalf("cell %d: Unicode/repeated prefixes distorted source coverage: %+v", index, report.Cells[index])
		}
	}
}

type suffixEmbeddings struct{}

func (suffixEmbeddings) Embed(_ context.Context, _ knowledge.EmbeddingConfig, texts []string) ([][]float32, error) {
	rows := make([][]float32, len(texts))
	for index, text := range texts {
		rows[index] = []float32{0, 1}
		if strings.Contains(text, "b") {
			rows[index] = []float32{1, 0}
		}
	}

	return rows, nil
}

func TestAnswerCannotBorrowAnUnselectedCopyOfRepeatedChunkText(t *testing.T) {
	dataset := tinyDataset()
	dataset.Documents[0].Lines = []string{strings.Repeat("a", 18) + "b"}
	dataset.Questions[0].Text = "b"
	dataset.Questions[0].Expected = []ragexperiment.Evidence{{Source: "faq.md", Text: strings.Repeat("a", 13) + "b"}}
	matrix := tinyMatrix()
	matrix.Chunking = []knowledge.Chunking{
		{Strategy: "paragraphs", MaxRunes: 6}, {Strategy: "windows", MaxRunes: 6},
	}
	options := fixtureOptions()
	options.Embedder, options.Settings.Embedding.Dimensions = suffixEmbeddings{}, 2

	report, err := ragexperiment.Run(context.Background(), dataset, matrix, options)
	if err != nil {
		t.Fatal(err)
	}

	for _, position := range []int{1, 3} {
		item := report.Cells[position].Scenarios[0]
		if *item.Recall != 0 || *item.AnswerCorrect {
			t.Fatalf("matching text in an unselected chunk must not fill the missing source span: %+v", item)
		}
	}
}

func TestMultipleSourcesForOneExamDoNotBecomeAmbiguousMetadata(t *testing.T) {
	dataset := tinyDataset()
	metadata := knowledge.Metadata{Topic: "preparation", Exam: "Exam A"}
	dataset.Documents[0].Metadata = metadata

	dataset.Documents = append(dataset.Documents, ragexperiment.Document{
		Source: "second.md", Lines: []string{"arrival"}, Metadata: metadata,
	})
	dataset.Questions[0].Filter = knowledge.Filter(metadata)
	if _, err := ragexperiment.Run(context.Background(), dataset, tinyMatrix(), fixtureOptions()); err != nil {
		t.Fatalf("documents sharing one metadata scope are not ambiguous entities: %v", err)
	}
}

func TestComparisonsIncludeScenarioOutcomeDifferences(t *testing.T) {
	report, err := ragexperiment.Run(context.Background(), tinyDataset(), tinyMatrix(), fixtureOptions())
	if err != nil {
		t.Fatal(err)
	}

	change := report.Comparisons[0]
	if change.Deltas.Passed != 1 || change.Deltas.Failed != -1 || change.Deltas.Errors != 0 ||
		change.Deltas.AnswerCorrect.Value == nil || *change.Deltas.AnswerCorrect.Value != 1 {
		t.Fatalf("k=3 adds one passing answer relative to the k=1 baseline: %+v", change)
	}
}
