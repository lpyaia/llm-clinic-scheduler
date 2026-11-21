// Command rag-compare compares chunking/top-k configurations on one controlled source snapshot.
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
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/openai"
	"github.com/lpyaia/llm-clinic-scheduler/internal/config"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/ragexperiment"
)

type options struct {
	dataset, matrix, output, mode, model, embedding string
	dimensions                                      int
	timeout                                         time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, &http.Client{Timeout: 45 * time.Second}))
}

func run(ctx context.Context, args []string, output, diagnostics io.Writer, client *http.Client) int {
	flags := flag.NewFlagSet("rag-compare", flag.ContinueOnError)
	flags.SetOutput(diagnostics)

	var opts options
	flags.StringVar(
		&opts.dataset,
		"dataset",
		"docs/evals/rag-strategies.pt-BR.json",
		"fixed documents and source evidence",
	)
	flags.StringVar(&opts.matrix, "matrix", "docs/evals/rag-matrix.json", "chunking and top-k matrix")
	flags.StringVar(&opts.output, "output", "", "JSON report path; defaults to stdout")
	flags.StringVar(&opts.mode, "mode", "fixture", "fixture or real (calls OpenAI for embeddings and generation)")
	flags.StringVar(&opts.model, "model", "", "generation model; real mode only")
	flags.StringVar(&opts.embedding, "embedding-model", "text-embedding-3-small", "real embedding model")
	flags.IntVar(&opts.dimensions, "dimensions", 1536, "real embedding dimensions")
	flags.DurationVar(&opts.timeout, "timeout", 10*time.Minute, "whole matrix deadline")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	if flags.NArg() != 0 || opts.timeout <= 0 || (opts.mode != "fixture" && opts.mode != "real") {
		_, _ = fmt.Fprintln(diagnostics, "require fixture or real mode, positive timeout, no positional args")
		return 2
	}

	ignored := false

	flags.Visit(func(flag *flag.Flag) {
		if opts.mode != "real" &&
			(flag.Name == "model" || flag.Name == "embedding-model" || flag.Name == "dimensions") {
			ignored = true
		}
	})

	if ignored {
		_, _ = fmt.Fprintln(diagnostics, "provider flags require explicit real mode")
		return 2
	}

	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	report, err := evaluate(ctx, opts, client)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	encoded = append(encoded, '\n')
	if opts.output == "" {
		_, err = output.Write(encoded)
	} else {
		err = os.WriteFile(opts.output, encoded, 0o600)
	}

	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	errors := 0

	for _, cell := range report.Cells {
		_, _ = fmt.Fprintf(diagnostics, "%s/%d/%d k=%d passed=%d failed=%d errors=%d\n",
			cell.Chunking.Strategy, cell.Chunking.MaxRunes, cell.Chunking.Overlap,
			cell.K, cell.Metrics.Passed, cell.Metrics.Failed, cell.Metrics.Errors)
		errors += cell.Metrics.Errors
	}

	if errors > 0 {
		return 2
	}

	return 0
}

func evaluate(ctx context.Context, opts options, client *http.Client) (ragexperiment.Report, error) {
	file, err := os.Open(opts.dataset)
	if err != nil {
		return ragexperiment.Report{}, err
	}
	defer func() { _ = file.Close() }()

	dataset, err := ragexperiment.Load(file)
	if err != nil {
		return ragexperiment.Report{}, err
	}

	matrixFile, err := os.Open(opts.matrix)
	if err != nil {
		return ragexperiment.Report{}, err
	}

	defer func() { _ = matrixFile.Close() }()

	matrix, err := ragexperiment.LoadMatrix(matrixFile)
	if err != nil {
		return ragexperiment.Report{}, err
	}

	providers, err := providerOptions(opts, client)
	if err != nil {
		return ragexperiment.Report{}, err
	}

	providers.Settings.Timeout = opts.timeout.String()

	revision, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err == nil {
		providers.Settings.CodeRevision = strings.TrimSpace(string(revision))
	} else {
		providers.Settings.CodeRevision = "unavailable"
	}

	status, err := exec.CommandContext(ctx, "git", "status", "--porcelain").Output()
	providers.Settings.Dirty = err != nil || len(status) > 0

	return ragexperiment.Run(ctx, dataset, matrix, providers)
}

func providerOptions(opts options, client *http.Client) (ragexperiment.Options, error) {
	providers := ragexperiment.Options{
		Embedder: ragexperiment.FixtureEmbeddings{}, Model: ragexperiment.FixtureModel{},
		Settings: ragexperiment.Settings{
			Mode:       opts.mode,
			Embedding:  knowledge.EmbeddingConfig{Provider: "fixture", Model: "lexical-v1", Dimensions: 7},
			Generation: ragexperiment.Generation{Provider: "fixture", Model: "first-three-v1"},
		},
	}
	if opts.mode != "real" {
		return providers, nil
	}

	if err := config.LoadEnv(); err != nil {
		return providers, err
	}

	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	model := strings.TrimSpace(opts.model)
	if model == "" {
		model = strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	}

	if key == "" || model == "" {
		return providers, errors.New("real mode requires OPENAI_API_KEY and OPENAI_MODEL or -model")
	}

	adapter := &openai.Client{APIKey: key, Model: model, HTTPClient: client}

	settings, err := json.Marshal(adapter.Configuration())
	if err != nil {
		return providers, err
	}

	providers.Embedder, providers.Model = &openai.Embeddings{APIKey: key, HTTPClient: client}, adapter
	providers.Settings.Embedding = knowledge.EmbeddingConfig{
		Provider:   "openai",
		Model:      opts.embedding,
		Dimensions: opts.dimensions,
	}
	providers.Settings.Generation = ragexperiment.Generation{Provider: "openai", Model: model, Settings: settings}

	return providers, nil
}
