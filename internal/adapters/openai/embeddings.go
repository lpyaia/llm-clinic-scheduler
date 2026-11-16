package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

// Embeddings implements the external embedding boundary independently of the conversation model.
type Embeddings struct {
	APIKey     string
	HTTPClient *http.Client
}

func (c *Embeddings) Embed(
	ctx context.Context, cfg knowledge.EmbeddingConfig, texts []string,
) ([][]float32, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	maxDimensions := map[string]int{"text-embedding-3-small": 1536, "text-embedding-3-large": 3072}
	if cfg.Provider != "openai" || cfg.Dimensions > maxDimensions[cfg.Model] ||
		strings.TrimSpace(c.APIKey) == "" || len(texts) == 0 || len(texts) > 256 {
		return nil, knowledge.ErrConfiguration
	}

	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			return nil, knowledge.ErrInput
		}
	}

	body, err := json.Marshal(struct {
		Model          string   `json:"model"`
		Input          []string `json:"input"`
		Dimensions     int      `json:"dimensions"`
		EncodingFormat string   `json:"encoding_format"`
	}{cfg.Model, texts, cfg.Dimensions, "float"})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		// Provider bodies can contain submitted content or credentials.
		return nil, fmt.Errorf("embedding provider HTTP status %d", response.StatusCode)
	}

	const maxBytes = 16 * 1024 * 1024

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}

	if len(payload) > maxBytes {
		return nil, knowledge.ErrEmbeddings
	}

	var wire struct {
		Model string `json:"model"`
		Data  []struct {
			Index     *int      `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, knowledge.ErrEmbeddings
	}

	if wire.Model != cfg.Model || len(wire.Data) != len(texts) {
		return nil, knowledge.ErrEmbeddings
	}

	vectors := make([][]float32, len(texts))
	for _, item := range wire.Data {
		if item.Index == nil || *item.Index < 0 || *item.Index >= len(texts) || vectors[*item.Index] != nil {
			return nil, knowledge.ErrEmbeddings
		}

		if err := cfg.ValidateVector(item.Embedding); err != nil {
			return nil, err
		}

		vectors[*item.Index] = item.Embedding
	}

	return vectors, nil
}
