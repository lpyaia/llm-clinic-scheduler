// Command eval runs Portuguese conversation evaluations without a database or HTTP server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/openai"
	"github.com/lpyaia/llm-clinic-scheduler/internal/config"
	"github.com/lpyaia/llm-clinic-scheduler/internal/evals"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, &http.Client{Timeout: 30 * time.Second}))
}

func run(ctx context.Context, args []string, output, diagnostics io.Writer, client *http.Client) int {
	flags := flag.NewFlagSet("eval", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	datasetPath := flags.String("dataset", "docs/evals/conversations.pt-BR.json", "scenario dataset JSON")
	mode := flags.String("mode", "scripted", "scripted, replay or real (real calls the provider)")
	replayPath := flags.String("replay", "", "previous report for replay mode")
	scenarioID := flags.String("scenario", "", "run only this scenario ID")
	modelName := flags.String("model", "", "real model name; defaults to OPENAI_MODEL")

	reportPath := flags.String("output", "", "report JSON path; defaults to stdout")
	timeout := flags.Duration("timeout", 2*time.Minute, "evaluation deadline; must be positive")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	if flags.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(diagnostics, "unexpected arguments or non-positive timeout")
		return 2
	}

	dataset, err := readDataset(*datasetPath, *scenarioID)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	options, err := modelOptions(*mode, *replayPath, *modelName, client)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	report, err := evals.Run(ctx, dataset, options)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	report.Configuration.Timeout = timeout.String()

	if *reportPath != "" {
		file, err := os.Create(*reportPath)
		if err != nil {
			_, _ = fmt.Fprintln(diagnostics, err)
			return 2
		}
		defer func() { _ = file.Close() }()

		output = file
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(report); err != nil {
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

func readDataset(path, scenarioID string) (evals.Dataset, error) {
	file, err := os.Open(path)
	if err != nil {
		return evals.Dataset{}, err
	}
	defer func() { _ = file.Close() }()

	dataset, err := evals.Load(file)
	if err != nil || scenarioID == "" {
		return dataset, err
	}

	for _, scenario := range dataset.Scenarios {
		if scenario.ID == scenarioID {
			dataset.Scenarios = []evals.Scenario{scenario}
			return dataset, nil
		}
	}

	return dataset, fmt.Errorf("scenario %q not found", scenarioID)
}

func modelOptions(mode, replayPath, modelName string, client *http.Client) (evals.Options, error) {
	options := evals.Options{Mode: mode}
	if replayPath != "" && mode != "replay" || modelName != "" && mode != "real" {
		return options, errors.New("-replay requires replay mode; -model requires explicit real mode")
	}

	if mode == "replay" {
		file, err := os.Open(replayPath)
		if err != nil {
			return options, err
		}
		defer func() { _ = file.Close() }()

		options.Recording = &evals.Report{}
		err = json.NewDecoder(file).Decode(options.Recording)

		return options, err
	}

	if mode != "real" {
		return options, nil
	}

	if err := config.LoadEnv(); err != nil {
		return options, err
	}

	if modelName == "" {
		modelName = os.Getenv("OPENAI_MODEL")
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	modelName = strings.TrimSpace(modelName)
	if apiKey == "" || modelName == "" {
		return options, errors.New("real mode requires OPENAI_API_KEY and -model or OPENAI_MODEL")
	}

	adapter := &openai.Client{APIKey: apiKey, Model: modelName, HTTPClient: client}

	settings, err := json.Marshal(adapter.Configuration())
	if err != nil {
		return options, err
	}

	options.Model = adapter
	options.Configuration = evals.Configuration{Provider: "openai", Model: modelName, Settings: settings}

	return options, nil
}
