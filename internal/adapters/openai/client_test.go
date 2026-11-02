package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/openai"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type localTransport struct{ target *url.URL }

func (t localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host

	return http.DefaultTransport.RoundTrip(clone)
}

func provider(t *testing.T, handler http.HandlerFunc) *openai.Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	return &openai.Client{
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: &http.Client{Transport: localTransport{target: target}},
	}
}

func TestProviderTranslatesToolCallsAndStructuredResponses(t *testing.T) {
	const answerSchema = `{"type":"object","properties":{},"additionalProperties":false}`

	client := provider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" ||
			r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("invalid provider request")
		}

		var in struct {
			Model    string `json:"model"`
			Messages []struct {
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name   string `json:"name"`
					Strict bool   `json:"strict"`
				} `json:"function"`
			} `json:"tools"`
			ResponseFormat *struct {
				Type   string `json:"type"`
				Schema struct {
					Name   string          `json:"name"`
					Strict bool            `json:"strict"`
					Schema json.RawMessage `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
			ToolChoice      *string `json:"tool_choice"`
			Parallel        bool    `json:"parallel_tool_calls"`
			Store           bool    `json:"store"`
			ReasoningEffort *string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}

		if in.Model != "test-model" || in.Parallel || in.Store || len(in.Tools) != 1 ||
			in.Tools[0].Type != "function" ||
			in.Tools[0].Function.Name != "list_appointments" ||
			!in.Tools[0].Function.Strict {
			t.Errorf("incorrect protocol mapping: %+v", in)
		}

		if in.ReasoningEffort != nil {
			t.Error("adapter must not send Luna's reasoning setting to other models")
		}

		if in.Messages[len(in.Messages)-1].Role == "user" {
			if in.ResponseFormat != nil || in.ToolChoice == nil || *in.ToolChoice != "required" {
				t.Errorf("initial request must select a tool without a final-answer schema: %+v", in)
			}

			_, _ = io.WriteString(
				w,
				`
{
  "choices": [
    {
      "finish_reason": "tool_calls",
      "message": {
        "role": "assistant",
        "content": null,
        "tool_calls": [
          {
            "id": "call-1",
            "type": "function",
            "function": {
              "name": "list_appointments",
              "arguments": "{}"
            }
          }
        ]
      }
    }
  ]
}
`,
			)

			return
		}

		if in.Messages[len(in.Messages)-1].ToolCallID != "call-1" {
			t.Error("tool result lost its call identifier")
		}

		if in.ToolChoice != nil || in.ResponseFormat == nil || in.ResponseFormat.Type != "json_schema" ||
			!in.ResponseFormat.Schema.Strict || in.ResponseFormat.Schema.Name != "custom_answer" ||
			string(in.ResponseFormat.Schema.Schema) != answerSchema {
			t.Errorf("continuation lost the caller's named schema: %+v", in)
		}

		_, _ = io.WriteString(
			w,
			`
{
  "choices": [
    {
      "finish_reason": "stop",
      "message": {
        "role": "assistant",
        "content": "{\"appointment_ids\":[]}"
      }
    }
  ]
}
`,
		)
	})
	in := agent.Request{
		Messages: []agent.Message{{Role: "user", Content: "Meus agendamentos"}},
		Tools: []agent.Tool{
			{
				Name:       "list_appointments",
				Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			},
		},
		RequireTool: true,
	}

	first, err := client.Complete(context.Background(), in)
	if err != nil || len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "list_appointments" ||
		string(first.ToolCalls[0].Arguments) != "{}" {
		t.Fatalf("first=%+v error=%v", first, err)
	}

	in.Messages = append(
		in.Messages,
		agent.Message{Role: "assistant", ToolCalls: first.ToolCalls},
		agent.Message{Role: "tool", ToolCallID: "call-1", Content: "[]"},
	)
	in.RequireTool = false
	in.ResponseSchema = &agent.ResponseSchema{Name: "custom_answer", Schema: json.RawMessage(answerSchema)}

	last, err := client.Complete(context.Background(), in)
	if err != nil || string(last.Content) != `{"appointment_ids":[]}` {
		t.Fatalf("last=%+v error=%v", last, err)
	}
}

func TestGPT6LunaCanCallToolsThroughChatCompletions(t *testing.T) {
	client := provider(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model           string          `json:"model"`
			ReasoningEffort string          `json:"reasoning_effort"`
			ToolChoice      string          `json:"tool_choice"`
			ResponseFormat  json.RawMessage `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}

		if request.ToolChoice != "required" || request.ResponseFormat != nil {
			t.Error("tool selection must not send an appointment response format")
		}

		// GPT-6 Luna requires reasoning_effort=none for Chat Completions tool calling.
		if request.Model != "gpt-6-luna" || request.ReasoningEffort != "none" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(
				w,
				`
{
  "error": {
    "type": "invalid_request_error",
    "param": "reasoning_effort",
    "message": "Tool calling requires reasoning_effort none."
  }
}
`,
			)

			return
		}

		_, _ = io.WriteString(
			w,
			`
{
  "choices": [
    {
      "finish_reason": "tool_calls",
      "message": {
        "role": "assistant",
        "content": null,
        "tool_calls": [
          {
            "id": "call-1",
            "type": "function",
            "function": {
              "name": "list_appointments",
              "arguments": "{}"
            }
          }
        ]
      }
    }
  ]
}
`,
		)
	})
	client.Model = "gpt-6-luna"
	in := agent.Request{
		Messages: []agent.Message{{Role: "user", Content: "Meus agendamentos"}},
		Tools: []agent.Tool{
			{
				Name:       "list_appointments",
				Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			},
		},
		RequireTool: true,
	}

	out, err := client.Complete(context.Background(), in)
	if err != nil || len(out.ToolCalls) != 1 || out.ToolCalls[0].Name != "list_appointments" {
		t.Fatalf("GPT-6 Luna tool call failed: response=%+v error=%v", out, err)
	}
}

func TestProviderRejectsMalformedOrIncompleteResponses(t *testing.T) {
	for _, payload := range []string{
		`{`, `null`, `{"choices":[]}`, `{"choices":[{},{}]}`,
		`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"{}"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"not JSON"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"role":"user","content":"{}"}}]}`,
		`
{
  "choices": [
    {
      "finish_reason": "stop",
      "message": {
        "role": "assistant",
        "refusal": "refused",
        "content": "{}"
      }
    }
  ]
}
`,
		`
{
  "choices": [
    {
      "finish_reason": "tool_calls",
      "message": {
        "role": "assistant",
        "tool_calls": [
          {
            "id": "call",
            "type": "function",
            "function": {
              "name": "list_appointments",
              "arguments": "{"
            }
          }
        ]
      }
    }
  ]
}
`,
		strings.Repeat(" ", 1024*1024+1),
	} {
		client := provider(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, payload) })
		_, err := client.Complete(context.Background(), agent.Request{})
		if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
			t.Fatalf("malformed response accepted: %v", err)
		}
	}
}

func TestProviderRejectsFinalAnswerWhenToolIsRequired(t *testing.T) {
	client := provider(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
  "choices": [
    {
      "finish_reason": "stop",
      "message": {
        "role": "assistant",
        "content": "{}"
      }
    }
  ]
}`)
	})

	_, err := client.Complete(context.Background(), agent.Request{
		Tools: []agent.Tool{{
			Name: "list_appointments", Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		}},
		RequireTool: true,
	})
	if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
		t.Fatalf("required tool call was bypassed by a final answer: %v", err)
	}
}

func TestProviderDoesNotExposeErrorBodies(t *testing.T) {
	client := provider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, "private provider diagnostic test-key")
	})
	_, err := client.Complete(context.Background(), agent.Request{})
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "test-key") {
		t.Fatalf("provider failure was exposed or ignored: %v", err)
	}
}

func TestProviderRequestHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	client := provider(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		close(started)

		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-started
		cancel()
	}()

	_, err := client.Complete(ctx, agent.Request{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("provider ignored cancellation: %v", err)
	}
}
