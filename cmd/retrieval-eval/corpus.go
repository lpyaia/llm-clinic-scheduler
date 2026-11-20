package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/openai"
	"github.com/lpyaia/llm-clinic-scheduler/internal/config"
	"github.com/lpyaia/llm-clinic-scheduler/internal/infrastructure/postgres"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/retrievaleval"
)

func realRankings(
	ctx context.Context, dataset retrievaleval.Dataset, settings retrievaleval.Configuration, client *http.Client,
) (rankings map[string][]knowledge.Result, err error) {
	if err := settings.Embedding.Validate(); err != nil {
		return nil, err
	}

	if err := config.LoadEnv(); err != nil {
		return nil, err
	}

	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil, errors.New("real mode requires OPENAI_API_KEY")
	}

	dsn := os.Getenv("RETRIEVAL_EVAL_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}

	sandbox, err := openCorpus(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, sandbox.Close()) }()

	service, err := knowledge.New(
		sandbox.store,
		&openai.Embeddings{APIKey: key, HTTPClient: client},
		settings.Embedding,
	)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)

	for _, doc := range dataset.Corpus.Documents {
		imported, err := service.Ingest(ctx, doc.Source, strings.NewReader(doc.Content), doc.Metadata)
		if err != nil {
			return nil, fmt.Errorf("ingest %q: %w", doc.Source, err)
		}

		counts[doc.Source] = imported.ChunkCount
	}

	for _, question := range dataset.Questions {
		for _, target := range question.Relevant {
			if target.Chunk > counts[target.Source] {
				return nil, fmt.Errorf("question %q: relevant chunk does not exist: %+v", question.ID, target)
			}
		}
	}

	rankings = make(map[string][]knowledge.Result)

	for _, question := range dataset.Questions {
		results, err := service.Search(ctx, question.Text, settings.K, question.Filter)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", question.ID, err)
		}

		rankings[question.ID] = results
	}

	return rankings, nil
}

// corpusSandbox owns a unique schema for one run. Close releases the store before dropping that schema.
// Ingestion and retrieval use the production service and repository; no application corpus is imported or searched.
type corpusSandbox struct {
	connection *pgx.Conn
	store      *postgres.Store
	schema     string
}

func openCorpus(ctx context.Context, dsn string) (*corpusSandbox, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path == "" {
		return nil, errors.New("real mode requires RETRIEVAL_EVAL_DATABASE_URL or TEST_DATABASE_URL (PostgreSQL URL)")
	}

	appURL := os.Getenv("DATABASE_URL")
	if appURL == "" {
		appURL = "postgres://localhost/clinic"
	}

	app, appErr := pgx.ParseConfig(appURL)
	evaluation, evalErr := pgx.ParseConfig(dsn)
	if appErr != nil || evalErr != nil || app.Database == evaluation.Database {
		return nil, errors.New("evaluation must use a database name different from DATABASE_URL")
	}

	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, errors.New("cannot connect to the evaluation database")
	}

	sandbox := &corpusSandbox{connection: connection}
	schema := "retrieval_eval_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		return nil, errors.Join(err, sandbox.Close())
	}

	sandbox.schema = schema
	query := u.Query()
	query.Set("search_path", schema)
	query.Set("connect_timeout", "5")
	u.RawQuery = query.Encode()

	sandbox.store, err = postgres.Open(u.String())
	if err != nil {
		return nil, errors.Join(errors.New("cannot open the isolated evaluation corpus"), sandbox.Close())
	}

	if err := sandbox.store.Migrate(ctx); err != nil {
		return nil, errors.Join(err, sandbox.Close())
	}

	return sandbox, nil
}

func (s *corpusSandbox) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var err error
	if s.store != nil {
		err = s.store.Close()
	}

	if s.schema != "" {
		_, dropErr := s.connection.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{s.schema}.Sanitize()+" CASCADE")
		err = errors.Join(err, dropErr)
	}

	return errors.Join(err, s.connection.Close(ctx))
}
