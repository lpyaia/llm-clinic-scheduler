package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/openai"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

func TestEmbeddingsPreserveInputOrder(t *testing.T) {
	chat := provider(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model          string   `json:"model"`
			Input          []string `json:"input"`
			Dimensions     int      `json:"dimensions"`
			EncodingFormat string   `json:"encoding_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}

		if r.Method != "POST" || r.URL.Path != "/v1/embeddings" ||
			r.Header.Get("Authorization") != "Bearer test-key" || request.Model != "text-embedding-3-small" ||
			request.Dimensions != 3 || request.EncodingFormat != "float" ||
			len(request.Input) != 2 || request.Input[0] != "parking" || request.Input[1] != "hours" {
			t.Errorf("unexpected embedding request: %+v", request)
		}

		_, _ = io.WriteString(w, `{
		  "model": "text-embedding-3-small",
		  "data": [
		    {"index": 1, "embedding": [0, 1, 0]},
		    {"index": 0, "embedding": [1, 0, 0]}
		  ]
		}`)
	})
	client := &openai.Embeddings{APIKey: chat.APIKey, HTTPClient: chat.HTTPClient}
	cfg := knowledge.EmbeddingConfig{Provider: "openai", Model: "text-embedding-3-small", Dimensions: 3}

	vectors, err := client.Embed(context.Background(), cfg, []string{"parking", "hours"})
	if err != nil || len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("unexpected vectors: %v, %v", vectors, err)
	}
}

func TestEmbeddingsRejectProviderFailuresAndMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"provider error", 429, "secret-provider-body"},
		{"invalid JSON", 200, "{"},
		{"missing vectors", 200, `{"model":"text-embedding-3-small","data":[]}`},
		{"wrong model", 200, `{"model":"other","data":[{"index":0,"embedding":[1,0,0]}]}`},
		{"wrong dimension", 200, `{"model":"text-embedding-3-small","data":[{"index":0,"embedding":[1,0]}]}`},
		{"zero vector", 200, `{"model":"text-embedding-3-small","data":[{"index":0,"embedding":[0,0,0]}]}`},
		{"missing index", 200, `{"model":"text-embedding-3-small","data":[{"embedding":[1,0,0]}]}`},
		{"negative index", 200, `{"model":"text-embedding-3-small","data":[{"index":-1,"embedding":[1,0,0]}]}`},
		{"invalid index", 200, `{"model":"text-embedding-3-small","data":[{"index":1,"embedding":[1,0,0]}]}`},
		{"oversized response", 200, strings.Repeat(" ", 16*1024*1024+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			chat := provider(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			})
			client := &openai.Embeddings{APIKey: chat.APIKey, HTTPClient: chat.HTTPClient}
			cfg := knowledge.EmbeddingConfig{Provider: "openai", Model: "text-embedding-3-small", Dimensions: 3}

			_, err := client.Embed(context.Background(), cfg, []string{"parking"})
			if err == nil || strings.Contains(err.Error(), "secret-provider-body") {
				t.Fatalf("expected safe provider failure, got %v", err)
			}
		})
	}
}

func TestEmbeddingsRejectDuplicateIndices(t *testing.T) {
	chat := provider(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
		  "model": "text-embedding-3-small",
		  "data": [
		    {"index": 0, "embedding": [1, 0, 0]},
		    {"index": 0, "embedding": [0, 1, 0]}
		  ]
		}`)
	})
	client := &openai.Embeddings{APIKey: chat.APIKey, HTTPClient: chat.HTTPClient}
	cfg := knowledge.EmbeddingConfig{Provider: "openai", Model: "text-embedding-3-small", Dimensions: 3}

	if _, err := client.Embed(context.Background(), cfg, []string{"parking", "hours"}); err == nil {
		t.Fatal("duplicate indices must not silently misassociate embeddings")
	}
}

func TestEmbeddingsRejectUnsupportedConfigurationAndCancelledRequests(t *testing.T) {
	client := &openai.Embeddings{APIKey: "test-key"}
	for _, cfg := range []knowledge.EmbeddingConfig{
		{Provider: "other", Model: "text-embedding-3-small", Dimensions: 3},
		{Provider: "openai", Model: "unknown", Dimensions: 3},
		{Provider: "openai", Model: "text-embedding-3-small", Dimensions: 1537},
	} {
		if _, err := client.Embed(context.Background(), cfg, []string{"parking"}); err == nil {
			t.Fatalf("accepted unsupported embedding configuration %+v", cfg)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := knowledge.EmbeddingConfig{Provider: "openai", Model: "text-embedding-3-small", Dimensions: 3}
	if _, err := client.Embed(ctx, cfg, []string{"parking"}); err == nil {
		t.Fatal("cancelled context must abort the provider request")
	}
}
