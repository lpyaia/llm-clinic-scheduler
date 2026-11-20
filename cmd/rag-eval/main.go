// Command rag-eval scores recorded extractive answers against a controlled RAG dataset.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lpyaia/llm-clinic-scheduler/internal/rageval"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, output, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("rag-eval", flag.ContinueOnError)
	flags.SetOutput(diagnostics)

	datasetPath := flags.String(
		"dataset",
		"docs/evals/rag-answers.pt-BR.json",
		"manual RAG reference labels and corpus",
	)
	responsesPath := flags.String(
		"responses",
		"docs/evals/rag-responses.fixture.json",
		"recorded responses and retrieval",
	)
	outputPath := flags.String("output", "", "JSON report path; defaults to stdout")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(diagnostics, "rag-eval does not accept positional arguments")
		return 2
	}

	report, err := evaluate(*datasetPath, *responsesPath)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	// Validate and serialize before opening output, preserving existing reports on invalid input.
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	encoded = append(encoded, '\n')
	if *outputPath != "" {
		err = os.WriteFile(*outputPath, encoded, 0o600)
	} else {
		_, err = output.Write(encoded)
	}

	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	_, _ = fmt.Fprintf(diagnostics, "passed=%d failed=%d errors=%d\n",
		report.Totals.Passed, report.Totals.Failed, report.Totals.Errors)

	if report.Totals.Errors > 0 {
		return 2
	}

	if report.Totals.Failed > 0 {
		return 1
	}

	return 0
}

func evaluate(datasetPath, responsesPath string) (rageval.Report, error) {
	file, err := os.Open(datasetPath)
	if err != nil {
		return rageval.Report{}, err
	}
	defer func() { _ = file.Close() }()

	dataset, err := rageval.Load(file)
	if err != nil {
		return rageval.Report{}, err
	}

	responses, err := os.Open(responsesPath)
	if err != nil {
		return rageval.Report{}, err
	}
	defer func() { _ = responses.Close() }()

	recording, err := rageval.LoadRun(responses)
	if err != nil {
		return rageval.Report{}, err
	}

	return rageval.Evaluate(dataset, recording)
}
