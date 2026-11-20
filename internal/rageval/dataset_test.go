package rageval_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/rageval"
)

func TestEvaluationRejectsUnusableLabelsAndIncompleteObservations(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*rageval.Dataset, *rageval.Run)
	}{
		{"version", func(d *rageval.Dataset, _ *rageval.Run) { d.Version = 2 }},
		{"no corpus", func(d *rageval.Dataset, _ *rageval.Run) { d.Corpus = nil }},
		{"no scenarios", func(d *rageval.Dataset, _ *rageval.Run) { d.Scenarios = nil }},
		{"duplicate scenario", func(d *rageval.Dataset, _ *rageval.Run) {
			d.Scenarios = append(d.Scenarios, d.Scenarios[0])
		}},
		{"no reference", func(d *rageval.Dataset, _ *rageval.Run) { d.Scenarios[0].ReferenceAnswer = " " }},
		{"no expected evidence", func(d *rageval.Dataset, _ *rageval.Run) {
			d.Scenarios[0].ExpectedEvidence = nil
		}},
		{"abstention with evidence", func(d *rageval.Dataset, _ *rageval.Run) {
			d.Scenarios[0].AbstentionExpected = true
		}},
		{"unknown expected evidence", func(d *rageval.Dataset, _ *rageval.Run) {
			d.Scenarios[0].ExpectedEvidence[0] = "unknown"
		}},
		{"duplicate evidence", func(d *rageval.Dataset, _ *rageval.Run) {
			d.Scenarios[0].ExpectedEvidence = []string{parkingID, parkingID}
		}},
		{"duplicate corpus", func(d *rageval.Dataset, _ *rageval.Run) { d.Corpus = append(d.Corpus, d.Corpus[0]) }},
		{"invalid location", func(d *rageval.Dataset, _ *rageval.Run) { d.Corpus[0].StartLine = 0 }},
		{"nonfinite score", func(d *rageval.Dataset, _ *rageval.Run) { d.Corpus[0].Similarity = math.NaN() }},
		{"missing response", func(_ *rageval.Dataset, r *rageval.Run) { delete(r.Responses, "parking") }},
		{"unknown response", func(_ *rageval.Dataset, r *rageval.Run) {
			r.Responses["unknown"] = r.Responses["parking"]
		}},
		{"unknown retrieved evidence", func(_ *rageval.Dataset, r *rageval.Run) {
			r.Responses["parking"] = rageval.Observation{
				Retrieved: []string{"unknown"}, MessageLines: []string{parkingText},
			}
		}},
		{"missing provenance", func(_ *rageval.Dataset, r *rageval.Run) { r.Provider = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataset := parkingDataset()
			run := rageval.Run{
				Provider: "fixture", Model: "manual-v1",
				Responses: map[string]rageval.Observation{"parking": {
					Retrieved: []string{parkingID}, MessageLines: []string{parkingText, parkingCitation},
				}},
			}

			test.change(&dataset, &run)
			run.DatasetSHA256, _ = dataset.Fingerprint()

			if _, err := rageval.Evaluate(dataset, run); err == nil {
				t.Fatal("invalid evaluation input accepted")
			}
		})
	}
}

func TestLoadingIsStrictAndChangedLabelsInvalidateRecordedAnswers(t *testing.T) {
	dataset := parkingDataset()
	hash, _ := dataset.Fingerprint()
	encoded, _ := json.Marshal(dataset)

	if _, err := rageval.Load(strings.NewReader(string(encoded))); err != nil {
		t.Fatal(err)
	}

	for _, suffix := range []string{` {}`, ` null`} {
		if _, err := rageval.Load(strings.NewReader(string(encoded) + suffix)); err == nil {
			t.Fatal("trailing JSON accepted")
		}
	}

	if _, err := rageval.Load(strings.NewReader(`{"unknown":true}`)); err == nil {
		t.Fatal("unknown dataset field accepted")
	}

	dataset.Scenarios[0].Question = "O estacionamento é gratuito?"

	_, err := rageval.Evaluate(dataset, rageval.Run{
		DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
		Responses: map[string]rageval.Observation{"parking": {
			Retrieved: []string{parkingID}, MessageLines: []string{parkingText, parkingCitation},
		}},
	})
	if err == nil {
		t.Fatal("changed question did not invalidate the recording fingerprint")
	}
}
