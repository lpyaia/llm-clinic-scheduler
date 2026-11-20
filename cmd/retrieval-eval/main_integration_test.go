//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lpyaia/llm-clinic-scheduler/internal/config"
	"github.com/lpyaia/llm-clinic-scheduler/internal/retrievaleval"
)

type redirectTransport struct{ target *url.URL }

func (transport redirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme, request.URL.Host = transport.target.Scheme, transport.target.Host

	return http.DefaultTransport.RoundTrip(request)
}

func TestRealCommandIngestsSearchesAndCleansIsolatedCorpus(t *testing.T) {
	if err := config.LoadEnv(); err != nil {
		t.Fatal(err)
	}

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("set TEST_DATABASE_URL to a dedicated test database")
	}

	t.Setenv("OPENAI_API_KEY", "retrieval-test-secret")
	t.Setenv("RETRIEVAL_EVAL_DATABASE_URL", dsn)
	dataset, _ := fixtureFiles(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.Background()) }()

	schemas := func() int {
		var count int
		if err := connection.QueryRow(ctx,
			"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'retrieval_eval_%'").Scan(&count); err != nil {
			t.Fatal(err)
		}

		return count
	}
	before := schemas()

	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "embedding failure"}[fail], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Model      string   `json:"model"`
					Input      []string `json:"input"`
					Dimensions int      `json:"dimensions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}

				if fail && len(request.Input) == 1 && request.Input[0] == "Is there a pool?" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}

				if r.URL.Path != "/v1/embeddings" || request.Dimensions != 3 ||
					r.Header.Get("Authorization") != "Bearer retrieval-test-secret" {
					t.Error("real mode must use the embedding adapter and requested configuration")
				}

				var data []map[string]any
				for index, text := range request.Input {
					vector := []float32{0, 1, 0}
					if strings.Contains(strings.ToLower(text), "ramp") {
						vector = []float32{1, 0, 0}
					}

					data = append(data, map[string]any{"index": index, "embedding": vector})
				}

				_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "data": data})
			}))
			defer server.Close()

			target, _ := url.Parse(server.URL)
			client := &http.Client{Transport: redirectTransport{target: target}, Timeout: 5 * time.Second}
			var output, diagnostics bytes.Buffer

			code := run(ctx, []string{"-dataset", dataset, "-mode", "real", "-k", "1", "-dimensions", "3"},
				&output, &diagnostics, client)
			if fail {
				if code != 2 || output.Len() != 0 {
					t.Fatalf("provider failure must not publish metrics: code=%d report=%s", code, output.String())
				}
			} else {
				var report retrievaleval.Report
				if err := json.Unmarshal(output.Bytes(), &report); err != nil {
					t.Fatalf("code=%d diagnostics=%s error=%v", code, diagnostics.String(), err)
				}

				if code != 0 || *report.Totals.MeanRecall != 1 || report.Configuration.Embedding.Dimensions != 3 ||
					report.Questions[0].Results[0].Ordinal != 2 || report.Questions[0].Results[0].Text != "Ramp." ||
					report.Totals.NoAnswerWithResults != 1 ||
					strings.Contains(output.String(), "retrieval-test-secret") {
					t.Fatalf("wrong real retrieval report: code=%d report=%+v", code, report)
				}
			}

			if schemas() != before {
				t.Fatal("temporary corpus schema leaked after run")
			}
		})
	}
}
