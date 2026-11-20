package retrievaleval_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/retrievaleval"
)

func TestRecallCountsDistinctRelevantSourcesWithinK(t *testing.T) {
	dataset := retrievaleval.Dataset{
		Version: 1,
		Corpus: retrievaleval.Corpus{ID: "controlled-v1", Documents: []retrievaleval.Document{
			{Source: "access.md", Content: "Parking.\n\nRamp."},
			{Source: "visit.md", Content: "Arrival."},
			{Source: "payment.md", Content: "Card."},
		}},
		Questions: []retrievaleval.Question{{
			ID: "visit", Text: "How do I arrive?",
			Relevant: []retrievaleval.Target{{Source: "access.md"}, {Source: "visit.md"}},
		}},
	}
	rankings := map[string][]knowledge.Result{"visit": {
		{Source: "access.md", Ordinal: 1},
		{Source: "access.md", Ordinal: 2},
		{Source: "payment.md", Ordinal: 1},
		{Source: "visit.md", Ordinal: 1},
	}}

	report, err := retrievaleval.Evaluate(dataset, rankings, retrievaleval.Configuration{Mode: "fixture", K: 3})
	if err != nil {
		t.Fatal(err)
	}

	question := report.Questions[0]
	if question.Recall == nil || *question.Recall != 0.5 || report.Totals.MeanRecall == nil ||
		*report.Totals.MeanRecall != 0.5 || len(question.Missing) != 1 ||
		question.Missing[0].Source != "visit.md" || len(question.Results) != 3 {
		t.Fatalf("expected recall 1/2 with visit.md missing at k=3: %+v", report)
	}

	if question.Results[1].Rank != 2 || !question.Results[1].Relevant || question.Results[2].Relevant ||
		len(report.Totals.WithMissing) != 1 || report.Totals.WithMissing[0] != "visit" ||
		len(report.Totals.WithIrrelevant) != 1 || report.Totals.WithIrrelevant[0] != "visit" {
		t.Fatalf("expected ranked relevance and drill-down IDs: %+v", report)
	}
}

func TestNoAnswerQuestionsStayOutsideRecallAndExposeRetrievedNoise(t *testing.T) {
	dataset := retrievaleval.Dataset{
		Version: 1,
		Corpus: retrievaleval.Corpus{ID: "controlled-v1", Documents: []retrievaleval.Document{
			{Source: "access.md", Content: "Parking.\n\nRamp."},
		}},
		Questions: []retrievaleval.Question{
			{ID: "ramp", Text: "Is there a ramp?", Relevant: []retrievaleval.Target{{Source: "access.md", Chunk: 2}}},
			{ID: "pool", Text: "Is there a pool?", NoAnswer: true, Relevant: []retrievaleval.Target{}},
			{ID: "gym", Text: "Is there a gym?", NoAnswer: true, Relevant: []retrievaleval.Target{}},
		},
	}
	rankings := map[string][]knowledge.Result{
		"ramp": {{Source: "access.md", Ordinal: 1}},
		"pool": {{Source: "access.md", Ordinal: 2}},
		"gym":  {},
	}

	report, err := retrievaleval.Evaluate(dataset, rankings, retrievaleval.Configuration{Mode: "fixture", K: 1})
	if err != nil {
		t.Fatal(err)
	}

	if *report.Totals.MeanRecall != 0 || report.Totals.Answerable != 1 || report.Totals.NoAnswer != 2 ||
		report.Totals.NoAnswerWithResults != 1 || report.Questions[1].Recall != nil ||
		report.Questions[2].Recall != nil || len(report.Questions[0].Missing) != 1 {
		t.Fatalf("unanswered questions must not inflate recall; wrong chunks must be irrelevant: %+v", report)
	}

	dataset.Questions = dataset.Questions[1:]

	delete(rankings, "ramp")

	report, err = retrievaleval.Evaluate(dataset, rankings, retrievaleval.Configuration{Mode: "fixture", K: 1})
	if err != nil || report.Totals.MeanRecall != nil {
		t.Fatalf("an entirely unanswered dataset must have null mean recall: %+v, %v", report, err)
	}

	encoded, err := json.Marshal(report)
	if err != nil || !strings.Contains(string(encoded), `"mean_recall_at_k":null`) {
		t.Fatalf("report must serialize without NaN or infinity: %s, %v", encoded, err)
	}
}

func TestReportFingerprintsCorpusAndLabelsForComparisons(t *testing.T) {
	dataset := smallDataset()
	config := retrievaleval.Configuration{Mode: "fixture", K: 1}
	rankings := map[string][]knowledge.Result{"ramp": {{Source: "access.md", Ordinal: 1}}}

	first, err := retrievaleval.Evaluate(dataset, rankings, config)
	if err != nil {
		t.Fatal(err)
	}

	dataset.Questions[0].Text = "Can I enter using a ramp?"

	second, err := retrievaleval.Evaluate(dataset, rankings, config)
	if err != nil {
		t.Fatal(err)
	}

	if len(first.DatasetSHA256) != 64 || first.DatasetSHA256 == second.DatasetSHA256 ||
		first.CorpusSHA256 != second.CorpusSHA256 || first.Corpus.ID != "one" ||
		first.Corpus.Documents[0].Content != "Ramp." {
		t.Fatalf("report must identify exact materials and labels: first=%+v second=%+v", first, second)
	}

	dataset.Corpus.Documents[0].Content = "Stairs."

	third, err := retrievaleval.Evaluate(dataset, rankings, config)
	if err != nil || third.CorpusSHA256 == second.CorpusSHA256 {
		t.Fatalf("changed document must change corpus fingerprint: %+v, %v", third, err)
	}
}
