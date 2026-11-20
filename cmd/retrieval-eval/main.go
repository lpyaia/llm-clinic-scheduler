// Command retrieval-eval evaluates ranked clinic materials against manual relevance labels.
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

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/retrievaleval"
)

type options struct {
	dataset, fixture, mode, output, model string
	k, dimensions                         int
	timeout                               time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, &http.Client{Timeout: 30 * time.Second}))
}

func run(ctx context.Context, args []string, output, diagnostics io.Writer, client *http.Client) int {
	flags := flag.NewFlagSet("retrieval-eval", flag.ContinueOnError)
	flags.SetOutput(diagnostics)

	var opts options
	flags.StringVar(&opts.dataset, "dataset", "docs/evals/retrieval.pt-BR.json", "controlled corpus and labels")
	flags.StringVar(&opts.fixture, "fixture", "docs/evals/retrieval-rankings.fixture.json", "controlled rankings")
	flags.StringVar(&opts.mode, "mode", "fixture", "fixture or real (calls OpenAI and isolated PostgreSQL)")
	flags.StringVar(&opts.output, "output", "", "JSON report path; defaults to stdout")
	flags.StringVar(&opts.model, "model", "text-embedding-3-small", "real embedding model")
	flags.IntVar(&opts.dimensions, "dimensions", 1536, "real embedding dimensions")
	flags.IntVar(&opts.k, "k", 3, "evaluate first k chunks (1..100)")
	flags.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "run deadline")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	if flags.NArg() != 0 || opts.k < 1 || opts.k > 100 || opts.timeout <= 0 ||
		(opts.mode != "fixture" && opts.mode != "real") {
		_, _ = fmt.Fprintln(
			diagnostics,
			"require mode fixture or real, k in 1..100, positive timeout, no positional args",
		)

		return 2
	}

	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	report, err := evaluate(ctx, opts, client)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, err)
		return 2
	}

	if opts.output != "" {
		file, err := os.Create(opts.output)
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

	_, _ = fmt.Fprintf(diagnostics, "answerable=%d no_answer=%d missing=%d irrelevant=%d\n",
		report.Totals.Answerable, report.Totals.NoAnswer,
		len(report.Totals.WithMissing), len(report.Totals.WithIrrelevant))

	return 0
}

func evaluate(ctx context.Context, opts options, client *http.Client) (retrievaleval.Report, error) {
	file, err := os.Open(opts.dataset)
	if err != nil {
		return retrievaleval.Report{}, err
	}
	defer func() { _ = file.Close() }()

	dataset, err := retrievaleval.Load(file)
	if err != nil {
		return retrievaleval.Report{}, err
	}

	settings := retrievaleval.Configuration{
		Mode: opts.mode, K: opts.k, Timeout: opts.timeout.String(),
		Retrieval: "controlled ranking fixture; no semantic quality evidence",
		Chunking:  "knowledge.Service: paragraphs, 1000 runes, no overlap, normalized line endings",
		Embedding: knowledge.EmbeddingConfig{Provider: "fixture", Model: "manual-rankings-v1", Dimensions: 0},
	}
	settings.CodeRevision, settings.Dirty = codeRevision(ctx)

	var rankings map[string][]knowledge.Result

	if opts.mode == "real" {
		settings.Embedding = knowledge.EmbeddingConfig{
			Provider:   "openai",
			Model:      opts.model,
			Dimensions: opts.dimensions,
		}
		settings.Retrieval = "PostgreSQL/pgvector exact cosine; metadata filters before global top-k; UUID/chunk ties"
		rankings, err = realRankings(ctx, dataset, settings, client)
	} else {
		rankings, err = fixtureRankings(opts.fixture, dataset)
	}

	if err != nil {
		return retrievaleval.Report{}, err
	}

	if err := ctx.Err(); err != nil {
		return retrievaleval.Report{}, err
	}

	return retrievaleval.Evaluate(dataset, rankings, settings)
}

func fixtureRankings(path string, dataset retrievaleval.Dataset) (map[string][]knowledge.Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var fixture struct {
		DatasetSHA256 string                        `json:"dataset_sha256"`
		Rankings      map[string][]knowledge.Result `json:"rankings"`
	}
	if err := retrievaleval.Decode(file, &fixture); err != nil {
		return nil, err
	}

	hash, err := dataset.Fingerprint()
	if err != nil {
		return nil, err
	}

	if hash != fixture.DatasetSHA256 {
		return nil, errors.New(
			"fixture dataset fingerprint differs; regenerate rankings for the current corpus and labels",
		)
	}

	return fixture.Rankings, nil
}

func codeRevision(ctx context.Context) (string, bool) {
	revision, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unavailable", false
	}

	status, err := exec.CommandContext(ctx, "git", "status", "--porcelain").Output()

	return strings.TrimSpace(string(revision)), err != nil || len(status) > 0
}
