package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/ragexperiment"
)

type redirectTransport struct{ target *url.URL }

func (t redirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme, request.URL.Host = t.target.Scheme, t.target.Host

	return http.DefaultTransport.RoundTrip(request)
}

func TestRealModeUsesBothOpenAIAdaptersAndKeepsProviderFailuresSeparate(t *testing.T) {
	dataset, matrix := comparisonFiles(t)
	t.Setenv("OPENAI_API_KEY", "test-secret-not-for-report")

	for _, phase := range []string{"success", "generation unavailable", "embeddings unavailable"} {
		t.Run(phase, func(t *testing.T) {
			var embeddings, generations atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-secret-not-for-report" {
					t.Error("missing API key")
				}

				w.Header().Set("Content-Type", "application/json")

				if r.URL.Path == "/v1/embeddings" {
					embeddings.Add(1)

					if phase == "embeddings unavailable" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}

					var request struct {
						Model      string
						Input      []string
						Dimensions int
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}

					if request.Dimensions != 7 {
						t.Error("wrong embedding configuration")
					}

					data := []map[string]any{}
					for index := range request.Input {
						data = append(data, map[string]any{"index": index, "embedding": []float32{1, 0, 0, 0, 0, 0, 0}})
					}

					_ = json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "data": data})

					return
				}

				if r.URL.Path != "/v1/chat/completions" {
					t.Error("unexpected endpoint")
					return
				}

				generations.Add(1)

				if phase == "generation unavailable" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}

				var request struct {
					Model    string
					Messages []struct {
						Role    string
						Content string
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}

				var excerpts []struct {
					ID string `json:"excerpt_id"`
				}
				if err := json.Unmarshal(
					[]byte(request.Messages[len(request.Messages)-1].Content),
					&excerpts,
				); err != nil {
					t.Error(err)
				}

				ids := []string{}
				for _, row := range excerpts {
					ids = append(ids, row.ID)
				}

				content, _ := json.Marshal(map[string]any{"excerpt_ids": ids})
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{
						"role": "assistant", "content": string(content),
					}}},
				})
			}))
			defer server.Close()

			target, _ := url.Parse(server.URL)
			client := &http.Client{Transport: redirectTransport{target}}

			var output, diagnostics bytes.Buffer

			code := run(context.Background(), []string{
				"-dataset", dataset, "-matrix", matrix, "-mode", "real", "-model", "gpt-6-luna", "-dimensions", "7",
			}, &output, &diagnostics, client)
			if phase == "embeddings unavailable" {
				if code != 2 || output.Len() != 0 || embeddings.Load() != 1 || generations.Load() != 0 {
					t.Fatalf("failed indexing must not publish comparison metrics: code=%d %s", code, &diagnostics)
				}

				return
			}

			var report ragexperiment.Report
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatalf("%s %v", &diagnostics, err)
			}

			wantCode := 0
			if phase == "generation unavailable" {
				wantCode = 2
			}

			if code != wantCode || embeddings.Load() != 8 || generations.Load() != 4 ||
				report.Settings.Embedding.Provider != "openai" || report.Settings.Generation.Model != "gpt-6-luna" ||
				strings.Contains(output.String(), "test-secret-not-for-report") {
				t.Fatalf("real adapter provenance/call isolation failed: code=%d calls=%d/%d %s",
					code, embeddings.Load(), generations.Load(), &diagnostics)
			}

			if phase == "generation unavailable" &&
				(report.Cells[0].Metrics.Errors != 1 || report.Cells[0].Metrics.AnswerCorrect.Value != nil) {
				t.Fatal("provider unavailability was treated as answer quality")
			}
		})
	}
}
