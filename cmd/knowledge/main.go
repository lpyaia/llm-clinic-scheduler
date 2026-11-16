package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/openai"
	"github.com/lpyaia/llm-clinic-scheduler/internal/config"
	"github.com/lpyaia/llm-clinic-scheduler/internal/infrastructure/postgres"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/timeouts"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 || (args[0] != "ingest" && args[0] != "search") {
		return errors.New("use: knowledge ingest -file <faq.md> | search -document <uuid> -question <text> [-limit 3]")
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(output)

	var (
		path, document, question string
		limit                    int
	)

	if args[0] == "ingest" {
		flags.StringVar(&path, "file", "", "UTF-8 .md or .txt document (maximum 64 KiB)")
	} else {
		flags.StringVar(&document, "document", "", "document UUID returned by ingest")
		flags.StringVar(&question, "question", "", "question to retrieve relevant excerpts")
		flags.IntVar(&limit, "limit", 3, "maximum results (1..100)")
	}

	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	if flags.NArg() != 0 || (args[0] == "ingest" && path == "") {
		return knowledge.ErrInput
	}

	if err := config.LoadEnv(); err != nil {
		return err
	}

	dimensions, err := strconv.Atoi(env("EMBEDDING_DIMENSIONS", "1536"))
	if err != nil {
		return knowledge.ErrConfiguration
	}

	embeddingConfig := knowledge.EmbeddingConfig{
		Provider: "openai", Model: env("EMBEDDING_MODEL", "text-embedding-3-small"), Dimensions: dimensions,
	}
	if err := embeddingConfig.Validate(); err != nil {
		return err
	}

	if os.Getenv("OPENAI_API_KEY") == "" {
		return errors.New("configure OPENAI_API_KEY for document and question embeddings")
	}

	timeoutConfig, err := timeouts.Load()
	if err != nil {
		return err
	}

	store, err := postgres.Open(env("DATABASE_URL", "postgres://clinic:clinic@localhost:5432/clinic?sslmode=disable"))
	if err != nil {
		return errors.New("cannot connect to PostgreSQL; check DATABASE_URL and the database service")
	}
	defer func() { _ = store.Close() }()

	service, err := knowledge.New(store, &openai.Embeddings{APIKey: os.Getenv("OPENAI_API_KEY")}, embeddingConfig)
	if err != nil {
		return err
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx, cancel := timeouts.Context(signalCtx, timeoutConfig.Command)
	defer cancel()

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")

	if args[0] == "ingest" {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("%w: cannot open document", knowledge.ErrExtraction)
		}
		defer func() { _ = file.Close() }()

		doc, err := service.Ingest(ctx, path, file)
		if err != nil {
			return err
		}

		return encoder.Encode(doc)
	}

	id, err := uuid.Parse(document)
	if err != nil {
		return knowledge.ErrInput
	}

	results, err := service.Search(ctx, id, question, limit)
	if err != nil {
		return err
	}

	return encoder.Encode(results)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
