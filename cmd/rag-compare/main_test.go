package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/ragexperiment"
)

func comparisonFiles(t *testing.T) (string, string) {
	t.Helper()

	dataset := ragexperiment.Dataset{
		Version: 1, CorpusID: "command-fixture",
		Documents: []ragexperiment.Document{{Source: "access.md", Lines: []string{"estacionamento", "", "rampa"}}},
		Questions: []ragexperiment.Question{{
			ID: "access", Text: "estacionamento rampa", Reference: "Há estacionamento e rampa.",
			Expected: []ragexperiment.Evidence{
				{Source: "access.md", Text: "estacionamento"}, {Source: "access.md", Text: "rampa"},
			},
		}},
	}
	matrix := ragexperiment.Matrix{
		Chunking: []knowledge.Chunking{{Strategy: "paragraphs", MaxRunes: 100}, {Strategy: "windows", MaxRunes: 8}},
		TopK:     []int{1, 3},
	}
	dir := t.TempDir()

	for name, value := range map[string]any{"dataset.json": dataset, "matrix.json": matrix} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, name), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return filepath.Join(dir, "dataset.json"), filepath.Join(dir, "matrix.json")
}

func TestOfflineCommandReproducesMatrixAndRecordsComparisonInputs(t *testing.T) {
	dataset, matrix := comparisonFiles(t)

	var previous []byte

	for range 2 {
		var output, diagnostics bytes.Buffer

		code := run(context.Background(), []string{"-dataset", dataset, "-matrix", matrix}, &output, &diagnostics, nil)
		if code != 0 {
			t.Fatalf("offline comparison failed: %d %s", code, &diagnostics)
		}

		var report ragexperiment.Report
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal(err)
		}

		if len(report.Cells) != 4 || len(report.Comparisons) != 3 || report.Settings.Mode != "fixture" ||
			len(report.DatasetSHA256) != 64 || report.Reranking != "none" {
			t.Fatalf("missing matrix/provenance: %+v", report)
		}

		if previous != nil && !bytes.Equal(previous, output.Bytes()) {
			t.Fatal("offline runs must be identical")
		}

		previous = append([]byte(nil), output.Bytes()...)
	}
}

func TestCommandRejectsConfigurationAndPreservesExistingReport(t *testing.T) {
	dataset, matrix := comparisonFiles(t)
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte("existing report"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, extra := range [][]string{
		{"-mode", "unknown"}, {"-model", "ignored"}, {"-timeout", "0s"}, {"unexpected"},
	} {
		args := []string{"-dataset", dataset, "-matrix", matrix, "-output", path}
		args = append(args, extra...)

		var output, diagnostics bytes.Buffer

		code := run(context.Background(), args, &output, &diagnostics, nil)
		encoded, err := os.ReadFile(path)
		if code != 2 || err != nil || string(encoded) != "existing report" || output.Len() != 0 {
			t.Fatalf("invalid input changed report: code=%d error=%v diagnostics=%s", code, err, &diagnostics)
		}
	}
}
