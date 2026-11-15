package openai_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
)

func TestProviderPreservesReportedUsageOnInvalidAnswers(t *testing.T) {
	client := provider(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
  "usage": {"prompt_tokens": 120, "completion_tokens": 30},
  "choices": [{"finish_reason":123}]
}`)
	})

	response, err := client.Complete(context.Background(), agent.Request{})
	if err == nil {
		t.Fatal("invalid answer must still fail")
	}

	usage := response.Usage
	if usage.Provider != "openai" || usage.Model != "test-model" || usage.Tokens == nil ||
		usage.Tokens.Input != 120 || usage.Tokens.Output != 30 {
		t.Fatalf("lost billable usage on invalid answer: %+v", usage)
	}
}

func TestProviderDistinguishesMissingUsageFromReportedZero(t *testing.T) {
	for _, test := range []struct {
		name, usage string
		available   bool
	}{
		{name: "absent"},
		{name: "null", usage: `,"usage":null`},
		{name: "partial", usage: `,"usage":{"prompt_tokens":4}`},
		{name: "negative", usage: `,"usage":{"prompt_tokens":-1,"completion_tokens":0}`},
		{name: "invalid", usage: `,"usage":{"prompt_tokens":"unknown","completion_tokens":0}`},
		{name: "zero", usage: `,"usage":{"prompt_tokens":0,"completion_tokens":0}`, available: true},
		{name: "reported", usage: `,"usage":{"prompt_tokens":120,"completion_tokens":30}`, available: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := provider(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"choices":[{
  "finish_reason":"stop","message":{"role":"assistant","content":"{}"}
}]`+test.usage+`}`)
			})

			response, err := client.Complete(context.Background(), agent.Request{})
			if err != nil || (response.Usage.Tokens != nil) != test.available {
				t.Fatalf("usage availability=%+v error=%v", response.Usage, err)
			}
		})
	}
}

func TestProviderFailureRetainsConfiguredIdentityWithoutInventingUsage(t *testing.T) {
	client := provider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"private-provider-data"}`)
	})

	response, err := client.Complete(context.Background(), agent.Request{})
	if err == nil || response.Usage.Provider != "openai" || response.Usage.Model != "test-model" ||
		response.Usage.Tokens != nil {
		t.Fatalf("failed call metadata=%+v error=%v", response.Usage, err)
	}
}
