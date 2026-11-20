package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
)

func TestProviderKnowledgeContinuationWithoutTools(t *testing.T) {
	const schema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "excerpt_ids": {
      "type": "array",
      "maxItems": 3,
      "items": {"type": "string"}
    }
  },
  "required": ["excerpt_ids"]
}`

	for _, tools := range [][]agent.Tool{nil, {}} {
		name := "nil tools"
		if tools != nil {
			name = "empty tools"
		}

		t.Run(name, func(t *testing.T) {
			client := provider(t, func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Tools    []json.RawMessage `json:"tools"`
					Parallel json.RawMessage   `json:"parallel_tool_calls"`
					Messages []struct {
						Role       string `json:"role"`
						ToolCallID string `json:"tool_call_id"`
					} `json:"messages"`
					ResponseFormat struct {
						Type       string `json:"type"`
						JSONSchema struct {
							Name   string          `json:"name"`
							Strict bool            `json:"strict"`
							Schema json.RawMessage `json:"schema"`
						} `json:"json_schema"`
					} `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}

				// This reproduces the provider's rejection, even when the supplied value is false.
				if len(request.Tools) == 0 && request.Parallel != nil {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{
  "error": {
    "param": "parallel_tool_calls",
    "message": "'parallel_tool_calls' is only allowed when 'tools' are specified."
  }
}`)

					return
				}

				if len(request.Tools) != 0 || len(request.Messages) != 6 ||
					request.Messages[5].Role != "tool" || request.Messages[5].ToolCallID != "knowledge" ||
					request.ResponseFormat.Type != "json_schema" ||
					request.ResponseFormat.JSONSchema.Name != "knowledge_answer" ||
					!request.ResponseFormat.JSONSchema.Strict {
					t.Errorf("continuation must preserve evidence and schema without enabling tools: %+v", request)
				}

				_, _ = io.WriteString(w, `{
  "choices": [{
    "finish_reason": "stop",
    "message": {"role":"assistant", "content":"{\"excerpt_ids\":[\"source:1\"]}"}
  }]
}`)
			})
			in := agent.Request{
				Tools: tools,
				Messages: []agent.Message{
					{Role: "system", Content: "Selecione os trechos que sustentam a resposta."},
					{Role: "user", Content: "Qual preparo para ultrassom?"},
					{Role: "assistant", Content: "Qual ultrassom: abdominal ou da tireoide?"},
					{Role: "user", Content: "Da tireoide."},
					{Role: "assistant", ToolCalls: []agent.ToolCall{{
						ID: "knowledge", Name: "search_clinic_knowledge", Arguments: json.RawMessage(`{}`),
					}}},
					{
						Role:       "tool",
						ToolCallID: "knowledge",
						Content:    `[{"excerpt_id":"source:1","text":"Formulário verde."}]`,
					},
				},
				ResponseSchema: &agent.ResponseSchema{Name: "knowledge_answer", Schema: json.RawMessage(schema)},
			}

			out, err := client.Complete(context.Background(), in)
			if err != nil || string(out.Content) != `{"excerpt_ids":["source:1"]}` {
				t.Fatalf("knowledge continuation failed: response=%+v error=%v", out, err)
			}
		})
	}
}
