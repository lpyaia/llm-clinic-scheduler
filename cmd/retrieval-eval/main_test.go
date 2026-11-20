package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/retrievaleval"
)

func fixtureFiles(t *testing.T) (string, string) {
	t.Helper()

	dataset := retrievaleval.Dataset{
		Version: 1,
		Corpus: retrievaleval.Corpus{ID: "cli-v1", Documents: []retrievaleval.Document{
			{Source: "access.md", Content: "Parking.\n\nRamp."},
		}},
		Questions: []retrievaleval.Question{
			{ID: "ramp", Text: "Is there a ramp?", Relevant: []retrievaleval.Target{{Source: "access.md", Chunk: 2}}},
			{ID: "pool", Text: "Is there a pool?", NoAnswer: true, Relevant: []retrievaleval.Target{}},
		},
	}

	hash, err := dataset.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	datasetPath, fixturePath := filepath.Join(dir, "dataset.json"), filepath.Join(dir, "fixture.json")
	for path, value := range map[string]any{
		datasetPath: dataset,
		fixturePath: map[string]any{
			"dataset_sha256": hash,
			"rankings": map[string][]knowledge.Result{
				"ramp": {{Source: "access.md", Ordinal: 1}, {Source: "access.md", Ordinal: 2}},
				"pool": {{Source: "access.md", Ordinal: 1}},
			},
		},
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return datasetPath, fixturePath
}

func TestCommandEvaluatesFixtureAndWritesInspectableReport(t *testing.T) {
	dataset, fixture := fixtureFiles(t)
	outputPath := filepath.Join(t.TempDir(), "report.json")

	var output, diagnostics bytes.Buffer

	code := run(context.Background(), []string{
		"-dataset", dataset, "-fixture", fixture, "-k", "1", "-output", outputPath,
	}, &output, &diagnostics, http.DefaultClient)
	if code != 0 {
		t.Fatalf("command failed (%d): %s", code, diagnostics.String())
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	var report retrievaleval.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}

	if report.Configuration.Mode != "fixture" || report.Configuration.K != 1 ||
		*report.Totals.MeanRecall != 0 || report.Totals.NoAnswerWithResults != 1 ||
		report.Configuration.Retrieval == "" || len(report.DatasetSHA256) != 64 || output.Len() != 0 {
		t.Fatalf("expected inspectable fixture report: %+v", report)
	}
}

func TestCommandRejectsInvalidInputWithoutPublishingReport(t *testing.T) {
	dataset, fixture := fixtureFiles(t)
	for _, args := range [][]string{
		{"-k", "0"}, {"-mode", "unknown"}, {"-timeout", "0s"}, {"unexpected"},
		{"-fixture", dataset},
	} {
		var output, diagnostics bytes.Buffer

		code := run(context.Background(), append([]string{"-dataset", dataset, "-fixture", fixture}, args...),
			&output, &diagnostics, http.DefaultClient)
		if code != 2 || output.Len() != 0 || strings.TrimSpace(diagnostics.String()) == "" {
			t.Fatalf("bad input must fail without a report: args=%v code=%d output=%s", args, code, output.String())
		}
	}
}

func TestBundledFixtureDemonstratesKnownRecallAndUnansweredCases(t *testing.T) {
	var output, diagnostics bytes.Buffer

	code := run(context.Background(), []string{
		"-dataset", "../../docs/evals/retrieval.pt-BR.json",
		"-fixture", "../../docs/evals/retrieval-rankings.fixture.json",
	}, &output, &diagnostics, http.DefaultClient)
	if code != 0 {
		t.Fatalf("bundled fixture failed: %s", diagnostics.String())
	}

	var report retrievaleval.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}

	if report.Totals.Answerable != 8 || report.Totals.NoAnswer != 3 ||
		*report.Totals.MeanRecall != 0.875 || len(report.Totals.WithMissing) != 2 ||
		report.Totals.NoAnswerWithResults != 2 {
		t.Fatalf("expected mean recall 7/8 at k=3 with two partial cases: %+v", report.Totals)
	}
}
