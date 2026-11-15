package trace

import (
	"math"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
)

type Metrics struct {
	TotalDurationNS int64 `json:"total_duration_ns"`
	ModelDurationNS int64 `json:"model_duration_ns"`
	ModelCalls      int   `json:"model_calls"`
	ToolCalls       int   `json:"tool_calls"`
	ToolFailures    int   `json:"tool_failures"`
	// ApplicationErrors counts failed turns once, rather than duplicating their failed steps.
	ApplicationErrors int          `json:"application_errors"`
	Steps             int          `json:"steps"`
	UsageStatus       string       `json:"usage_status"`
	InputTokens       *int64       `json:"input_tokens"`
	OutputTokens      *int64       `json:"output_tokens"`
	MissingUsageCalls int          `json:"missing_usage_calls"`
	UsageOverflow     bool         `json:"usage_overflow,omitempty"`
	Cost              CostEstimate `json:"estimated_cost"`
}

type CostEstimate struct {
	Status   string `json:"status"`
	Currency string `json:"currency,omitempty"`
	// Amount is nil unless the whole execution can be estimated.
	Amount            *float64 `json:"amount"`
	KnownAmount       *float64 `json:"known_amount"`
	MissingPriceCalls int      `json:"missing_price_calls"`
	Overflow          bool     `json:"overflow,omitempty"`
}

// RecordModel attaches metadata independently of content capture, even on error.
// The turn owner must call it before End; a panic still counts the open model step.
func (s *Span) RecordModel(usage contract.ModelUsage) {
	if s == nil {
		return
	}

	redactor := s.run.recorder.redactor

	usage.Provider = redactor.text(usage.Provider)
	usage.Model = redactor.text(usage.Model)
	if usage.Tokens != nil {
		tokens := *usage.Tokens
		usage.Tokens = &tokens
	}

	s.run.data.Steps[s.index].Model = &usage
}

func (r *Run) metrics() Metrics {
	m := Metrics{TotalDurationNS: r.data.DurationNS, Steps: len(r.data.Steps)}
	if r.data.Outcome != "ok" {
		m.ApplicationErrors = 1
	}

	pricing := r.recorder.options.Pricing
	if pricing != nil {
		m.Cost.Currency = pricing.currency
	}

	knownUsage, knownCost := 0, 0

	for _, step := range r.data.Steps {
		switch step.Kind {
		case "tool":
			m.ToolCalls++
			if step.Outcome != "ok" {
				m.ToolFailures++
			}
		case "model":
			m.ModelCalls++
			m.ModelDurationNS += step.DurationNS

			priced := false

			var price rate
			if pricing != nil && step.Model != nil {
				price, priced = pricing.rates[modelKey{step.Model.Provider, step.Model.Model}]
			}

			if !priced {
				m.Cost.MissingPriceCalls++
			}

			if step.Model == nil || step.Model.Tokens == nil ||
				step.Model.Tokens.Input < 0 || step.Model.Tokens.Output < 0 {
				m.MissingUsageCalls++

				continue
			}

			tokens := step.Model.Tokens

			if m.InputTokens == nil {
				m.InputTokens, m.OutputTokens = new(int64), new(int64)
			}

			if tokens.Input > math.MaxInt64-*m.InputTokens || tokens.Output > math.MaxInt64-*m.OutputTokens {
				m.UsageOverflow = true
			} else {
				*m.InputTokens += tokens.Input
				*m.OutputTokens += tokens.Output
			}

			knownUsage++

			if priced {
				if m.Cost.add(tokens, price) {
					knownCost++
				}
			}
		}
	}

	m.UsageStatus = availability(knownUsage, m.ModelCalls)
	if m.UsageOverflow {
		m.UsageStatus = "unavailable"
		m.InputTokens, m.OutputTokens = nil, nil
	}

	m.Cost.Status = availability(knownCost, m.ModelCalls)
	if m.Cost.Status == "complete" {
		m.Cost.Amount = m.Cost.KnownAmount
	}

	return m
}

func (c *CostEstimate) add(tokens *contract.TokenUsage, price rate) bool {
	input := float64(tokens.Input) / 1e6 * (*price.Input)
	output := float64(tokens.Output) / 1e6 * (*price.Output)

	cost := input + output
	if c.KnownAmount != nil {
		cost += *c.KnownAmount
	}

	if math.IsInf(cost, 0) || math.IsNaN(cost) {
		c.Overflow = true

		return false
	}

	c.KnownAmount = &cost

	return true
}

func availability(known, total int) string {
	switch {
	case total == 0:
		return "not_applicable"
	case known == total:
		return "complete"
	case known == 0:
		return "unavailable"
	default:
		return "partial"
	}
}
