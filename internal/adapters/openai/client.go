// Package openai implements the model boundary with Chat Completions.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

const maxResponseBytes = 1024 * 1024

type Client struct {
	APIKey, Model string
	HTTPClient    *http.Client
}

type function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Refusal    *string    `json:"refusal,omitempty"`
}

func (c *Client) Complete(ctx context.Context, in agent.Request) (agent.Response, error) {
	if strings.TrimSpace(c.APIKey) == "" || strings.TrimSpace(c.Model) == "" {
		return agent.Response{}, errors.New("configure OPENAI_API_KEY e OPENAI_MODEL")
	}

	var request struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
		Tools    []struct {
			Type     string   `json:"type"`
			Function function `json:"function"`
		} `json:"tools"`
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string          `json:"name"`
				Strict bool            `json:"strict"`
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
		ParallelToolCalls   bool   `json:"parallel_tool_calls"`
		Store               bool   `json:"store"`
		MaxCompletionTokens int    `json:"max_completion_tokens"`
		ReasoningEffort     string `json:"reasoning_effort,omitempty"`
	}

	request.Model = c.Model
	request.MaxCompletionTokens = 2048
	request.ResponseFormat.Type = "json_schema"
	request.ResponseFormat.JSONSchema.Name = "appointment_answer"

	request.ResponseFormat.JSONSchema.Strict = true
	request.ResponseFormat.JSONSchema.Schema = in.ResponseSchema
	if c.Model == "gpt-6-luna" {
		// Chat Completions tool calling requires reasoning to be disabled for Luna.
		request.ReasoningEffort = "none"
	}

	for _, item := range in.Messages {
		wire := message{Role: item.Role, ToolCallID: item.ToolCallID}
		if len(item.ToolCalls) == 0 || item.Content != "" {
			wire.Content = &item.Content
		}

		for _, call := range item.ToolCalls {
			encoded := toolCall{ID: call.ID, Type: "function"}
			encoded.Function.Name = call.Name
			encoded.Function.Arguments = string(call.Arguments)
			wire.ToolCalls = append(wire.ToolCalls, encoded)
		}

		request.Messages = append(request.Messages, wire)
	}

	for _, tool := range in.Tools {
		request.Tools = append(request.Tools, struct {
			Type     string   `json:"type"`
			Function function `json:"function"`
		}{
			Type: "function",
			Function: function{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
				Strict:      true,
			},
		})
	}

	body, err := json.Marshal(request)
	if err != nil {
		return agent.Response{}, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://api.openai.com/v1/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return agent.Response{}, err
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return agent.Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Provider bodies can contain credentials or patient data; retain only the status.
		return agent.Response{}, fmt.Errorf("model provider HTTP status %d", resp.StatusCode)
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return agent.Response{}, err
	}

	if len(payload) > maxResponseBytes {
		return agent.Response{}, malformed()
	}

	var response struct {
		Choices []struct {
			FinishReason string  `json:"finish_reason"`
			Message      message `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &response) != nil || len(response.Choices) != 1 {
		return agent.Response{}, malformed()
	}

	choice := response.Choices[0]
	if choice.Message.Role != "assistant" || choice.Message.Refusal != nil {
		return agent.Response{}, malformed()
	}

	out := agent.Response{}

	if len(choice.Message.ToolCalls) > 0 {
		if choice.FinishReason != "tool_calls" ||
			(choice.Message.Content != nil && strings.TrimSpace(*choice.Message.Content) != "") {
			return agent.Response{}, malformed()
		}

		for _, call := range choice.Message.ToolCalls {
			if call.Type != "function" || call.ID == "" || call.Function.Name == "" ||
				!json.Valid([]byte(call.Function.Arguments)) {
				return agent.Response{}, malformed()
			}

			out.ToolCalls = append(
				out.ToolCalls,
				agent.ToolCall{
					ID:        call.ID,
					Name:      call.Function.Name,
					Arguments: json.RawMessage(call.Function.Arguments),
				},
			)
		}

		return out, nil
	}

	if choice.FinishReason != "stop" || choice.Message.Content == nil || !json.Valid([]byte(*choice.Message.Content)) {
		return agent.Response{}, malformed()
	}

	out.Content = json.RawMessage(*choice.Message.Content)

	return out, nil
}

func malformed() error {
	return domain.Failure("MODEL_INVALID_RESPONSE", "O assistente retornou uma resposta inválida. Tente novamente.")
}
