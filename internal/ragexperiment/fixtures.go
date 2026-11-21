package ragexperiment

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

// FixtureEmbeddings uses fixed lexical coordinates plus a nonzero bias, never semantic embeddings.
type FixtureEmbeddings struct{}

func (FixtureEmbeddings) Embed(
	ctx context.Context,
	config knowledge.EmbeddingConfig,
	texts []string,
) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if config.Dimensions != 7 {
		return nil, knowledge.ErrConfiguration
	}

	terms := []string{"estacionamento", "rampa", "documento", "ultrassom", "jejum", "convênio"}

	rows := make([][]float32, len(texts))
	for index, text := range texts {
		vector := make([]float32, 7)
		for position, term := range terms {
			vector[position] = float32(strings.Count(strings.ToLower(text), term))
		}

		vector[6] = 0.1
		rows[index] = vector
	}

	return rows, nil
}

// FixtureModel deliberately selects the first three retrieved excerpts, even when irrelevant.
// This fixed behavior makes recall/answer regressions observable without claiming language quality.
type FixtureModel struct{}

func (FixtureModel) Complete(ctx context.Context, request agent.Request) (agent.Response, error) {
	if err := ctx.Err(); err != nil {
		return agent.Response{}, err
	}

	var excerpts []struct {
		ID string `json:"excerpt_id"`
	}
	if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &excerpts); err != nil {
		return agent.Response{}, err
	}

	ids := []string{}
	for _, excerpt := range excerpts[:min(3, len(excerpts))] {
		ids = append(ids, excerpt.ID)
	}

	content, err := json.Marshal(map[string]any{"excerpt_ids": ids})

	return agent.Response{
		Content: content, Usage: agent.ModelUsage{Provider: "fixture", Model: "first-three-v1"},
	}, err
}
