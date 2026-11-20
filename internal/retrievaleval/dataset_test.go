package retrievaleval_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/retrievaleval"
)

func smallDataset() retrievaleval.Dataset {
	return retrievaleval.Dataset{
		Version: 1,
		Corpus: retrievaleval.Corpus{ID: "one", Documents: []retrievaleval.Document{
			{Source: "access.md", Content: "Ramp."},
		}},
		Questions: []retrievaleval.Question{{
			ID: "ramp", Text: "Is there a ramp?", Relevant: []retrievaleval.Target{{Source: "access.md"}},
		}},
	}
}

func TestLoadRejectsLabelsThatWouldSilentlyDistortRecall(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*retrievaleval.Dataset)
	}{
		{"unlabelled", func(d *retrievaleval.Dataset) { d.Questions[0].Relevant = nil }},
		{"no answer with source", func(d *retrievaleval.Dataset) { d.Questions[0].NoAnswer = true }},
		{"unknown source", func(d *retrievaleval.Dataset) { d.Questions[0].Relevant[0].Source = "typo.md" }},
		{"negative chunk", func(d *retrievaleval.Dataset) { d.Questions[0].Relevant[0].Chunk = -1 }},
		{"duplicate question", func(d *retrievaleval.Dataset) {
			d.Questions = append(d.Questions, d.Questions[0])
		}},
		{"overlapping labels", func(d *retrievaleval.Dataset) {
			d.Questions[0].Relevant = append(
				d.Questions[0].Relevant,
				retrievaleval.Target{Source: "access.md", Chunk: 1},
			)
		}},
		{"duplicate labels", func(d *retrievaleval.Dataset) {
			d.Questions[0].Relevant = append(d.Questions[0].Relevant, d.Questions[0].Relevant[0])
		}},
		{"duplicate document", func(d *retrievaleval.Dataset) {
			d.Corpus.Documents = append(d.Corpus.Documents, d.Corpus.Documents[0])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataset := smallDataset()
			test.change(&dataset)

			encoded, err := json.Marshal(dataset)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := retrievaleval.Load(strings.NewReader(string(encoded))); err == nil {
				t.Fatal("invalid dataset accepted")
			}
		})
	}
}

func TestEvaluateRejectsMissingOrInvalidRankings(t *testing.T) {
	for _, rankings := range []map[string][]knowledge.Result{
		{},
		{"ramp": {}, "unknown": {}},
		{"ramp": {{Source: "typo.md", Ordinal: 1}}},
		{"ramp": {{Source: "access.md", Ordinal: 0}}},
		{"ramp": {{Source: "access.md", Ordinal: 1}, {Source: "access.md", Ordinal: 1}}},
	} {
		if _, err := retrievaleval.Evaluate(smallDataset(), rankings,
			retrievaleval.Configuration{Mode: "fixture", K: 3}); err == nil {
			t.Fatalf("invalid or incomplete rankings accepted: %+v", rankings)
		}
	}

	for _, k := range []int{0, -1, 101} {
		if _, err := retrievaleval.Evaluate(smallDataset(), map[string][]knowledge.Result{"ramp": {}},
			retrievaleval.Configuration{Mode: "fixture", K: k}); err == nil {
			t.Fatalf("invalid k=%d accepted", k)
		}
	}
}
