package ragexperiment

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

type span struct {
	source     string
	start, end int
}

func targets(dataset Dataset, question Question) []span {
	var spans []span

	for _, evidence := range question.Expected {
		for _, document := range dataset.Documents {
			if document.Source != evidence.Source {
				continue
			}

			text := strings.Join(document.Lines, "\n")
			start := utf8.RuneCountInString(text[:strings.Index(text, evidence.Text)])
			spans = append(spans, span{evidence.Source, start, start + utf8.RuneCountInString(evidence.Text)})
		}
	}

	return spans
}

func selectedSpans(index []Passage, ids []string) []span {
	var spans []span

	for _, passage := range index {
		if slices.Contains(ids, rowID(passage.Result)) {
			spans = append(spans, span{passage.Source, passage.StartRune, passage.EndRune})
		}
	}

	slices.SortFunc(spans, func(a, b span) int { return a.start - b.start })

	return spans
}

func covers(target span, selected []span) bool {
	cursor := target.start
	for _, part := range selected {
		if part.source != target.source || part.end <= cursor {
			continue
		}

		if part.start > cursor {
			return false
		}

		cursor = part.end
		if cursor >= target.end {
			return true
		}
	}

	return false
}

func scoreRetrieval(dataset Dataset, index []Passage, question Question, rows []knowledge.Result) Scenario {
	item := Scenario{
		Question: question, Status: "passed", Retrieved: rows,
		Missing: []Evidence{}, MessageLines: []string{}, SelectedEvidence: []string{},
	}
	selected := selectedSpans(index, resultIDs(rows))
	for position, target := range targets(dataset, question) {
		if !covers(target, selected) {
			item.Missing = append(item.Missing, question.Expected[position])
		}
	}

	if !question.NoAnswer {
		recall := float64(len(question.Expected)-len(item.Missing)) / float64(len(question.Expected))
		item.Recall = &recall
	}

	return item
}

func scoreAnswer(dataset Dataset, index []Passage, item *Scenario) {
	selected := selectedSpans(index, item.SelectedEvidence)

	expected := targets(dataset, item.Question)
	correct := len(item.Assessment.UnsupportedClaims) == 0
	for _, target := range expected {
		correct = correct && covers(target, selected)
	}

	for _, part := range selected {
		relevant := false
		for _, target := range expected {
			relevant = relevant ||
				(part.source == target.source && part.start < target.end && part.end > target.start)
		}

		correct = correct && relevant
	}

	abstentionCorrect := item.Assessment.Abstained == item.Question.NoAnswer

	correct = correct && abstentionCorrect
	item.AnswerCorrect, item.AbstentionCorrect = &correct, &abstentionCorrect
	if !correct || !abstentionCorrect || len(item.Assessment.Failures) > 0 {
		item.Status = "failed"
	}
}

func add(metric *Metric, value *float64) {
	if value == nil {
		return
	}

	sum := *value
	if metric.Value != nil {
		sum += *metric.Value * float64(metric.Count)
	}

	metric.Count++
	mean := sum / float64(metric.Count)
	metric.Value = &mean
}

func boolean(value *bool) *float64 {
	if value == nil {
		return nil
	}

	score := 0.0
	if *value {
		score = 1
	}

	return &score
}

func summarize(scenarios []Scenario) Metrics {
	var metrics Metrics
	for _, item := range scenarios {
		add(&metrics.Recall, item.Recall)
		add(&metrics.Groundedness, item.Assessment.Groundedness)
		add(&metrics.CitationCorrectness, item.Assessment.CitationCorrectness)
		add(&metrics.AnswerCorrect, boolean(item.AnswerCorrect))
		add(&metrics.AbstentionCorrect, boolean(item.AbstentionCorrect))

		metrics.UnsupportedClaims += len(item.Assessment.UnsupportedClaims)
		switch item.Status {
		case "passed":
			metrics.Passed++
		case "failed":
			metrics.Failed++
		case "error":
			metrics.Errors++
		}
	}

	return metrics
}

func addDelta(metric *Metric, before, after *float64) {
	if before == nil || after == nil {
		return
	}

	delta := *after - *before
	add(metric, &delta)
}

func compare(baseline, cell Cell) Comparison {
	result := Comparison{BaselineID: baseline.IndexID, IndexID: cell.IndexID}
	result.Deltas.Passed = cell.Metrics.Passed - baseline.Metrics.Passed
	result.Deltas.Failed = cell.Metrics.Failed - baseline.Metrics.Failed
	result.Deltas.Errors = cell.Metrics.Errors - baseline.Metrics.Errors
	result.Deltas.UnsupportedClaims = cell.Metrics.UnsupportedClaims - baseline.Metrics.UnsupportedClaims

	for position, after := range cell.Scenarios {
		before := baseline.Scenarios[position]
		addDelta(&result.Deltas.Recall, before.Recall, after.Recall)
		addDelta(&result.Deltas.Groundedness, before.Assessment.Groundedness, after.Assessment.Groundedness)
		addDelta(
			&result.Deltas.CitationCorrectness,
			before.Assessment.CitationCorrectness,
			after.Assessment.CitationCorrectness,
		)
		addDelta(&result.Deltas.AnswerCorrect, boolean(before.AnswerCorrect), boolean(after.AnswerCorrect))
		addDelta(&result.Deltas.AbstentionCorrect, boolean(before.AbstentionCorrect), boolean(after.AbstentionCorrect))
	}

	return result
}
