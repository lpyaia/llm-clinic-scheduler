package ragexperiment_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/ragexperiment"
)

func TestRejectsAmbiguousReferencesAndInvalidMatricesBeforeCallingProviders(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ragexperiment.Dataset, *ragexperiment.Matrix)
	}{
		{"duplicate question", func(d *ragexperiment.Dataset, _ *ragexperiment.Matrix) {
			d.Questions = append(d.Questions, d.Questions[0])
		}},
		{"unknown source", func(d *ragexperiment.Dataset, _ *ragexperiment.Matrix) {
			d.Questions[0].Expected[0].Source = "unknown.md"
		}},
		{"ambiguous literal", func(d *ragexperiment.Dataset, _ *ragexperiment.Matrix) {
			d.Documents[0].Lines = append(d.Documents[0].Lines, "parking")
		}},
		{"no answer with evidence", func(d *ragexperiment.Dataset, _ *ragexperiment.Matrix) {
			d.Questions[0].NoAnswer = true
		}},
		{"missing label", func(d *ragexperiment.Dataset, _ *ragexperiment.Matrix) { d.Questions[0].Expected = nil }},
		{"single strategy", func(_ *ragexperiment.Dataset, m *ragexperiment.Matrix) {
			m.Chunking[1] = knowledge.Chunking{Strategy: "paragraphs", MaxRunes: 5}
		}},
		{"invalid overlap", func(_ *ragexperiment.Dataset, m *ragexperiment.Matrix) { m.Chunking[1].Overlap = 5 }},
		{"duplicate k", func(_ *ragexperiment.Dataset, m *ragexperiment.Matrix) { m.TopK = []int{1, 1} }},
		{"invalid k", func(_ *ragexperiment.Dataset, m *ragexperiment.Matrix) { m.TopK = []int{1, 101} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataset, matrix := tinyDataset(), tinyMatrix()
			test.change(&dataset, &matrix)

			if _, err := ragexperiment.Run(context.Background(), dataset, matrix, fixtureOptions()); err == nil {
				t.Fatal("invalid experiment accepted")
			}
		})
	}
}

func TestDatasetAndMatrixLoadingRejectUnknownFieldsAndTrailingJSON(t *testing.T) {
	encoded, _ := json.Marshal(tinyDataset())
	if _, err := ragexperiment.Load(strings.NewReader(string(encoded))); err != nil {
		t.Fatal(err)
	}

	for _, input := range []string{string(encoded) + " {}", `{"unexpected":true}`} {
		if _, err := ragexperiment.Load(strings.NewReader(input)); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}

	encoded, _ = json.Marshal(tinyMatrix())
	if _, err := ragexperiment.LoadMatrix(strings.NewReader(string(encoded))); err != nil {
		t.Fatal(err)
	}

	if _, err := ragexperiment.LoadMatrix(strings.NewReader(string(encoded) + " null")); err == nil {
		t.Fatal("trailing matrix JSON accepted")
	}
}

func TestCancellationProducesNoPartialComparison(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := ragexperiment.Run(ctx, tinyDataset(), tinyMatrix(), fixtureOptions())
	if err == nil || len(report.Cells) != 0 {
		t.Fatal("cancelled run published partial metrics")
	}
}
