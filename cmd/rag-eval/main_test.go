package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/rageval"
)

func TestKnownFixturesProduceInspectableScenarioClassifications(t *testing.T) {
	var output, diagnostics bytes.Buffer

	code := run([]string{
		"-dataset", "../../docs/evals/rag-answers.pt-BR.json",
		"-responses", "../../docs/evals/rag-responses.fixture.json",
	}, &output, &diagnostics)
	if code != 2 {
		t.Fatalf("the deliberate unavailable fixture requires exit 2, got %d: %s", code, &diagnostics)
	}

	var report rageval.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}

	if len(report.Scenarios) != 12 || report.Totals != (rageval.Totals{Passed: 3, Failed: 8, Errors: 1}) ||
		report.Provider != "fixture" || report.Model != "manual-extractive-v1" || report.Rubric == "" ||
		report.Limitations == "" || len(report.Corpus) != 3 || len(report.DatasetSHA256) != 64 {
		t.Fatalf("unexpected fixture classification or missing provenance: %+v", report)
	}

	wantFailures := map[string]string{
		"plausible-unsupported": "unsupported_claim", "invented-citation": "citation_nonexistent",
		"source-not-retrieved": "citation_not_retrieved", "wrong-citation": "citation_no_support",
		"wrong-location": "citation_location_mismatch", "irrelevant-grounded-answer": "answer_incorrect",
		"undue-abstention": "abstention_incorrect", "missing-citation": "citation_missing",
	}
	for _, scenario := range report.Scenarios {
		wanted, negative := wantFailures[scenario.ID]
		if !negative {
			continue
		}

		found := false
		for _, failure := range scenario.Failures {
			found = found || failure.Kind == wanted
		}

		if !found || scenario.Status != "failed" || scenario.Question == "" || scenario.ReferenceAnswer == "" ||
			len(scenario.MessageLines) == 0 {
			t.Fatalf("fixture %s must expose %s and its inputs: %+v", scenario.ID, wanted, scenario)
		}

		if scenario.ID == "irrelevant-grounded-answer" &&
			(*scenario.Groundedness != 1 || *scenario.CitationCorrectness != 1 || *scenario.AnswerCorrect) {
			t.Fatalf("faithful irrelevant evidence must fail only answer correctness: %+v", scenario)
		}
	}
}

func TestCommandExitCodesAndOutputFile(t *testing.T) {
	encoded, err := os.ReadFile("../../docs/evals/rag-answers.pt-BR.json")
	if err != nil {
		t.Fatal(err)
	}

	dataset, err := rageval.Load(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}

	dataset.Scenarios = dataset.Scenarios[:1]
	hash, _ := dataset.Fingerprint()

	for _, test := range []struct {
		name string
		code int
	}{
		{"passed", 0}, {"failed", 1}, {"error", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()

			observation := rageval.Observation{
				Retrieved: []string{"aa000000-0000-0000-0000-000000000001:1"},
				MessageLines: []string{
					"Há estacionamento gratuito na entrada da clínica.",
					"Fonte: faq.md (documento aa000000-0000-0000-0000-000000000001, trecho 1, linhas 2–2).",
				},
			}

			switch test.code {
			case 1:
				observation.MessageLines = []string{"O estacionamento custa R$ 20."}
			case 2:
				observation.Error = "provider unavailable"
			}

			writeJSON(t, filepath.Join(dir, "dataset.json"), dataset)
			writeJSON(t, filepath.Join(dir, "responses.json"), rageval.Run{
				DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
				Responses: map[string]rageval.Observation{"parking-correct": observation},
			})

			var output, diagnostics bytes.Buffer

			code := run([]string{
				"-dataset", filepath.Join(dir, "dataset.json"), "-responses", filepath.Join(dir, "responses.json"),
				"-output", filepath.Join(dir, "report.json"),
			}, &output, &diagnostics)
			report, err := os.ReadFile(filepath.Join(dir, "report.json"))
			if err != nil || code != test.code || output.Len() != 0 || !json.Valid(report) {
				t.Fatalf("want report and exit %d: got %d, %v, %s", test.code, code, err, &diagnostics)
			}
		})
	}
}

func TestInvalidInputDoesNotOverwriteExistingReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte("existing report"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output, diagnostics bytes.Buffer

	code := run([]string{
		"-dataset", "missing.json", "-output", path,
	}, &output, &diagnostics)
	data, err := os.ReadFile(path)
	if err != nil || code != 2 || string(data) != "existing report" || output.Len() != 0 {
		t.Fatalf("invalid input must preserve output: %d, %s, %v", code, data, err)
	}

	for _, args := range [][]string{{"-help"}, {"-unknown"}, {"unexpected"}} {
		output.Reset()
		diagnostics.Reset()

		code := run(args, &output, &diagnostics)
		if (args[0] == "-help" && code != 0) || (args[0] != "-help" && code != 2) ||
			strings.TrimSpace(diagnostics.String()) == "" {
			t.Fatalf("unexpected flag handling: %v exit=%d diagnostics=%s", args, code, &diagnostics)
		}
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
