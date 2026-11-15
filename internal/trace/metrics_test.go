package trace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/trace"
)

func TestMetricsAggregateControlledDurationsUsageAndCost(t *testing.T) {
	prices, err := trace.LoadPricing(strings.NewReader(`{
  "currency": "USD",
  "rates": [{"provider":"test","model":"model","input_per_million":2,"output_per_million":8}]
}`))
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer

	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	recorder := trace.NewJSONL(&output, trace.Options{Pricing: prices, Now: func() time.Time { return now }})
	run := recorder.Start(context.Background(), "private patient message")
	first := run.StartStep(trace.Step{Kind: "model"}, nil)
	now = now.Add(100 * time.Millisecond)

	first.RecordModel(contract.ModelUsage{Provider: "test", Model: "model",
		Tokens: &contract.TokenUsage{Input: 1000, Output: 100}})
	first.End(nil, nil)

	tool := run.StartStep(trace.Step{Kind: "tool"}, nil)
	now = now.Add(50 * time.Millisecond)

	tool.End(nil, nil)

	second := run.StartStep(trace.Step{Kind: "model"}, nil)
	now = now.Add(200 * time.Millisecond)

	second.RecordModel(contract.ModelUsage{Provider: "test", Model: "model",
		Tokens: &contract.TokenUsage{Input: 500, Output: 50}})
	second.End(nil, nil)

	now = now.Add(50 * time.Millisecond)

	run.Finish(nil, nil)

	var execution trace.Execution
	if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}

	m := execution.Metrics
	if m.TotalDurationNS != 400000000 || m.ModelDurationNS != 300000000 ||
		m.ModelCalls != 2 || m.ToolCalls != 1 || m.ToolFailures != 0 || m.ApplicationErrors != 0 || m.Steps != 3 {
		t.Fatalf("incorrect effort or durations: %+v", m)
	}

	if m.UsageStatus != "complete" || m.InputTokens == nil || *m.InputTokens != 1500 ||
		m.OutputTokens == nil || *m.OutputTokens != 150 || m.MissingUsageCalls != 0 {
		t.Fatalf("incorrect aggregate consumption: %+v", m)
	}

	if m.Cost.Status != "complete" || m.Cost.Amount == nil || *m.Cost.Amount != 0.0042 ||
		m.Cost.Currency != "USD" || execution.Steps[0].Model == nil || execution.TraceID == "" {
		t.Fatalf("cost must be 0.003 input + 0.0012 output, correlated to steps: %+v", m.Cost)
	}

	if bytes.Contains(output.Bytes(), []byte("private patient message")) {
		t.Fatal("metrics exposed patient content")
	}
}

func TestMetricsCostAvailabilityAndProviderModelLookup(t *testing.T) {
	pricing, err := trace.LoadPricing(strings.NewReader(`{
  "currency":"BRL",
  "rates":[
    {"provider":"one","model":"shared","input_per_million":2,"output_per_million":8},
    {"provider":"two","model":"shared","input_per_million":4,"output_per_million":16},
    {"provider":"one","model":"free","input_per_million":0,"output_per_million":0}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, costStatus, usageStatus string
		calls                         []contract.ModelUsage
		missingPrices, missingUsage   int
		knownCost                     *float64
	}{
		{name: "no calls", costStatus: "not_applicable", usageStatus: "not_applicable"},
		{name: "unknown usage", costStatus: "unavailable", usageStatus: "unavailable", missingUsage: 1,
			calls: []contract.ModelUsage{{Provider: "one", Model: "shared"}}},
		{name: "unknown price", costStatus: "unavailable", usageStatus: "complete", missingPrices: 1,
			calls: []contract.ModelUsage{{Provider: "missing", Model: "shared",
				Tokens: &contract.TokenUsage{Input: 1000, Output: 100}}}},
		{name: "partial price", costStatus: "partial", usageStatus: "complete", missingPrices: 1,
			knownCost: new(0.0028), calls: []contract.ModelUsage{
				{Provider: "one", Model: "shared", Tokens: &contract.TokenUsage{Input: 1000, Output: 100}},
				{Provider: "missing", Model: "shared", Tokens: &contract.TokenUsage{}},
			}},
		{name: "partial usage", costStatus: "partial", usageStatus: "partial", missingUsage: 1,
			knownCost: new(0.0028), calls: []contract.ModelUsage{
				{Provider: "one", Model: "shared", Tokens: &contract.TokenUsage{Input: 1000, Output: 100}},
				{Provider: "one", Model: "shared"},
			}},
		{name: "different provider", costStatus: "complete", usageStatus: "complete",
			knownCost: new(0.0056), calls: []contract.ModelUsage{
				{Provider: "two", Model: "shared", Tokens: &contract.TokenUsage{Input: 1000, Output: 100}},
			}},
		{name: "explicit free price", costStatus: "complete", usageStatus: "complete",
			knownCost: new(0.0), calls: []contract.ModelUsage{
				{Provider: "one", Model: "free", Tokens: &contract.TokenUsage{Input: 1000, Output: 100}},
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer

			run := trace.NewJSONL(&output, trace.Options{Pricing: pricing}).Start(context.Background(), "private")
			for _, usage := range test.calls {
				step := run.StartStep(trace.Step{Kind: "model"}, nil)
				step.RecordModel(usage)
				step.End(nil, errors.New("provider failed after reporting usage"))
			}

			run.Finish(nil, errors.New("failed execution"))

			var execution trace.Execution
			if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
				t.Fatal(err)
			}

			m := execution.Metrics
			if m.Cost.Status != test.costStatus || m.UsageStatus != test.usageStatus ||
				m.MissingUsageCalls != test.missingUsage || m.Cost.MissingPriceCalls != test.missingPrices ||
				m.ModelCalls != len(test.calls) || m.ApplicationErrors != 1 ||
				(m.Cost.Amount != nil) != (test.costStatus == "complete") {
				t.Fatalf("incorrect missing-data semantics: %+v", m)
			}

			if (m.Cost.KnownAmount == nil) != (test.knownCost == nil) ||
				(test.knownCost != nil && *m.Cost.KnownAmount != *test.knownCost) {
				t.Fatalf("known subtotal=%v expected=%v", m.Cost.KnownAmount, test.knownCost)
			}

			if test.usageStatus == "unavailable" || test.usageStatus == "not_applicable" {
				if m.InputTokens != nil || m.OutputTokens != nil {
					t.Fatal("unknown consumption became zero")
				}
			}
		})
	}
}

func TestPricingRejectsAmbiguousOrInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"currency":"USD","rates":[]}`,
		`{"currency":"usd","rates":[{}]}`,
		`{"currency":"USD","rates":[{"provider":"p","model":"m","input_per_million":1}]}`,
		`{"currency":"USD","rates":[
  {"provider":"p","model":"m","input_per_million":-1,"output_per_million":1}
]}`,
		`{"currency":"USD","rates":[
  {"provider":"p","model":"m","input_per_million":1e999,"output_per_million":1}
]}`,
		`{"currency":"USD","rates":[
  {"provider":"p","model":"m","input_per_million":1,"output_per_million":1},
  {"provider":"p","model":"m","input_per_million":2,"output_per_million":2}
]}`,
		`{"currency":"USD","rates":[],"private-credential":"secret"}`,
		`{} {}`,
	} {
		_, err := trace.LoadPricing(strings.NewReader(raw))
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid table must fail without exposing its contents: %v", err)
		}
	}
}

func TestMetricsKeepTraceExportableWhenAggregatesOverflow(t *testing.T) {
	for _, test := range []struct {
		name          string
		rate          float64
		first, second int64
	}{
		{name: "cost", rate: 1e308, first: 1000000, second: 1000000},
		{name: "tokens", rate: 1, first: math.MaxInt64, second: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			pricing, err := trace.LoadPricing(strings.NewReader(fmt.Sprintf(`{
  "currency":"USD",
  "rates":[{"provider":"p","model":"m","input_per_million":%g,"output_per_million":0}]
}`, test.rate)))
			if err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer

			recorder := trace.NewJSONL(&output, trace.Options{Pricing: pricing})

			run := recorder.Start(context.Background(), "private")
			for _, input := range []int64{test.first, test.second} {
				step := run.StartStep(trace.Step{Kind: "model"}, nil)
				step.RecordModel(contract.ModelUsage{Provider: "p", Model: "m",
					Tokens: &contract.TokenUsage{Input: input}})
				step.End(nil, nil)
			}

			run.Finish(nil, nil)

			var execution trace.Execution
			if err := json.Unmarshal(output.Bytes(), &execution); err != nil || recorder.Failures() != 0 {
				t.Fatalf("overflow lost the execution: export failures=%d error=%v", recorder.Failures(), err)
			}

			m := execution.Metrics
			if m.ModelCalls != 2 || len(execution.Steps) != 2 {
				t.Fatal("overflow lost call counts or trace steps")
			}

			if test.name == "tokens" && (m.UsageStatus != "unavailable" || m.InputTokens != nil ||
				m.OutputTokens != nil || !m.UsageOverflow) {
				t.Fatalf("overflowed tokens must not become valid negative consumption: %+v", m)
			}

			if test.name == "cost" && (m.Cost.Status != "partial" || m.Cost.Amount != nil ||
				m.Cost.KnownAmount == nil || *m.Cost.KnownAmount != 1e308 || !m.Cost.Overflow) {
				t.Fatalf("overflowed cost must retain a finite partial subtotal: %+v", m.Cost)
			}
		})
	}
}
