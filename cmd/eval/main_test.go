package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/evals"
)

type redirectTransport struct{ target *url.URL }

func (transport redirectTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme, request.URL.Host = transport.target.Scheme, transport.target.Host
	return http.DefaultTransport.RoundTrip(request)
}

func TestExplicitRealModeUsesAdapterAndReportsItsConfiguration(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-secret-not-for-reports")

	var received map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" ||
			request.Header.Get("Authorization") != "Bearer test-secret-not-for-reports" {
			t.Error("real mode must use the authenticated Chat Completions adapter")
		}

		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{
		  "role":"assistant","tool_calls":[{"id":"contact","type":"function","function":{
		    "name":"offer_human_contact","arguments":"{\"reason\":\"medical_advice\"}"
		  }}]
		}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`))
	}))
	defer server.Close()

	target, _ := url.Parse(server.URL)
	client := &http.Client{Transport: redirectTransport{target: target}, Timeout: 5 * time.Second}

	var output, diagnostics bytes.Buffer

	code := run(context.Background(), []string{
		"-dataset", "../../docs/evals/conversations.pt-BR.json", "-scenario", "medical-refusal",
		"-mode", "real", "-model", "gpt-6-luna",
	}, &output, &diagnostics, client)
	if code != 0 {
		var failed evals.Report

		_ = json.Unmarshal(output.Bytes(), &failed)
		for _, scenario := range failed.Scenarios {
			for _, turn := range scenario.Turns {
				t.Logf("error=%s failures=%v calls=%d", turn.Error, turn.Failures, len(turn.Calls))

				for _, call := range turn.Calls {
					t.Logf("completion=%+v", call.Response)
				}
			}
		}

		t.Fatalf("real evaluation failed (%d): %s", code, diagnostics.String())
	}

	var report evals.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}

	if report.Configuration.Provider != "openai" || report.Configuration.Model != "gpt-6-luna" ||
		report.Configuration.Mode != "real" || report.Totals.Passed != 1 || len(report.Scenarios) != 1 ||
		len(report.Scenarios[0].Turns[0].Calls[0].Request.Messages) == 0 ||
		strings.Contains(output.String(), "test-secret-not-for-reports") {
		t.Fatalf("missing evaluation provenance or secret exposed: config=%+v totals=%+v",
			report.Configuration, report.Totals)
	}

	var settings map[string]any
	if err := json.Unmarshal(report.Configuration.Settings, &settings); err != nil {
		t.Fatal(err)
	}

	if received["model"] != "gpt-6-luna" || received["reasoning_effort"] != "none" ||
		settings["reasoning_effort"] != received["reasoning_effort"] ||
		settings["max_completion_tokens"] != received["max_completion_tokens"] ||
		settings["store"] != received["store"] || settings["parallel_tool_calls"] != received["parallel_tool_calls"] {
		t.Fatal("report must describe the adapter's actual wire configuration")
	}
}

func TestCommandDefaultsToOfflineAndReplaysItsReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")

	var output, diagnostics bytes.Buffer

	code := run(context.Background(), []string{
		"-dataset", "../../docs/evals/conversations.pt-BR.json", "-output", path,
	}, &output, &diagnostics, nil)
	if code != 0 || output.Len() != 0 {
		t.Fatalf("default offline evaluation should save a report: code=%d diagnostics=%s", code, &diagnostics)
	}

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var original evals.Report
	if err := json.Unmarshal(encoded, &original); err != nil {
		t.Fatal(err)
	}

	code = run(context.Background(), []string{
		"-dataset", "../../docs/evals/conversations.pt-BR.json", "-mode", "replay", "-replay", path,
	}, &output, &diagnostics, nil)

	var replayed evals.Report
	if err := json.Unmarshal(output.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}

	if code != 0 || original.Configuration.Mode != "scripted" || replayed.Configuration.Mode != "replay" ||
		original.Totals.Passed != len(original.Scenarios) || replayed.Totals.Passed != len(original.Scenarios) {
		t.Fatalf("offline and replay must pass without a provider client: code=%d diagnostics=%s", code, &diagnostics)
	}
}

func TestCommandExitCodesDistinguishBehaviorAndExecutionFailures(t *testing.T) {
	encoded, err := os.ReadFile("../../docs/evals/conversations.pt-BR.json")
	if err != nil {
		t.Fatal(err)
	}

	var dataset evals.Dataset
	if err := json.Unmarshal(encoded, &dataset); err != nil {
		t.Fatal(err)
	}

	dataset.Scenarios = dataset.Scenarios[:1]

	for _, test := range []struct {
		name string
		code int
	}{
		{name: "behavior", code: 1},
		{name: "execution", code: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "behavior" {
				dataset.Scenarios[0].Turns[0].Expect.Response.Contains = []string{"This phrase must be missing"}
			} else {
				dataset.Scenarios[0].Turns[0].Responses = []evals.Completion{{Error: "provider unavailable"}}
			}

			path := filepath.Join(t.TempDir(), "dataset.json")
			data, _ := json.Marshal(dataset)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}

			var output, diagnostics bytes.Buffer

			code := run(context.Background(), []string{"-dataset", path}, &output, &diagnostics, nil)
			if code != test.code || !json.Valid(output.Bytes()) {
				t.Fatalf("want exit %d with a report, got %d: %s", test.code, code, &diagnostics)
			}
		})
	}
}

func TestReplayRejectsAnIgnoredRealModelFlag(t *testing.T) {
	var output, diagnostics bytes.Buffer

	code := run(context.Background(), []string{
		"-dataset", "../../docs/evals/conversations.pt-BR.json", "-mode", "replay",
		"-replay", "../../docs/evals/conversations.pt-BR.json", "-model", "gpt-6-luna",
	}, &output, &diagnostics, nil)
	if code != 2 || !strings.Contains(diagnostics.String(), "-model requires explicit real mode") {
		t.Fatalf("reject ignored real-model flags before reading a replay: code=%d diagnostics=%s", code, &diagnostics)
	}
}
