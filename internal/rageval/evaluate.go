package rageval

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

const answerPrefix = "Encontrei nos materiais da clínica:"

const abstention = "Não encontrei essa informação nos materiais da clínica."

const unavailable = "Não consegui consultar os materiais da clínica agora. Tente novamente mais tarde."

var citationIdentity = regexp.MustCompile(`^Fonte: (.+) \(documento ([0-9a-f-]{36}), trecho ([1-9][0-9]*), .+\)\.$`)

// Evaluate scores complete extractive passages, rather than inferring semantic entailment.
func Evaluate(dataset Dataset, run Run) (Report, error) {
	if err := dataset.validate(); err != nil {
		return Report{}, err
	}

	if err := validateRun(dataset, run); err != nil {
		return Report{}, err
	}

	report := Report{
		DatasetSHA256: run.DatasetSHA256, Provider: run.Provider, Model: run.Model, Corpus: dataset.Corpus,
		Rubric: "extractive-v1: exact passages; manual relevant evidence; exact citation identities and locations",
		Limitations: "No model judge. Exact extractive passages only; paraphrases require manual review. " +
			"Fixture results verify classification, not live retrieval or model language quality.",
		Scenarios: []ScenarioReport{},
	}

	for _, scenario := range dataset.Scenarios {
		result := evaluateScenario(dataset, scenario, run.Responses[scenario.ID])
		report.Scenarios = append(report.Scenarios, result)

		switch result.Status {
		case "passed":
			report.Totals.Passed++
		case "failed":
			report.Totals.Failed++
		case "error":
			report.Totals.Errors++
		}
	}

	return report, nil
}

func evaluateScenario(dataset Dataset, scenario Scenario, observation Observation) ScenarioReport {
	result := ScenarioReport{
		Scenario: scenario, Observation: observation, Status: "passed",
		UnsupportedClaims: []string{}, Failures: []Failure{},
	}
	message := strings.TrimSpace(strings.Join(observation.MessageLines, "\n"))
	if observation.Error != "" || message == unavailable {
		result.Status = "error"
		if result.Error == "" {
			result.Error = "knowledge retrieval unavailable"
		}

		return result
	}

	claims := parseClaims(message, dataset.Corpus)
	matched := make(map[string]bool)
	supported, cited := 0, 0
	allRelevant := true

	for _, claim := range claims {
		isSupported := false
		isRelevant := false

		for _, row := range dataset.Corpus {
			if claim.text == strings.TrimSpace(row.Text) {
				if slices.Contains(scenario.ExpectedEvidence, evidenceID(row)) {
					matched[evidenceID(row)] = true
					isRelevant = true
				}

				isSupported = isSupported || slices.Contains(observation.Retrieved, evidenceID(row))
			}
		}

		allRelevant = allRelevant && isRelevant

		if isSupported {
			supported++
		} else {
			result.UnsupportedClaims = append(result.UnsupportedClaims, claim.text)
			result.Failures = append(result.Failures, Failure{Kind: "unsupported_claim", Claim: claim.text})
		}

		if failure := checkCitation(claim, dataset.Corpus, observation.Retrieved); failure != "" {
			result.Failures = append(result.Failures, Failure{
				Kind: failure, Claim: claim.text, Citation: claim.citation,
			})
		} else {
			cited++
		}
	}

	correct := len(matched) == len(scenario.ExpectedEvidence) && allRelevant &&
		(message == abstention) == scenario.AbstentionExpected
	for _, id := range scenario.ExpectedEvidence {
		correct = correct && matched[id]
	}

	abstentionCorrect := (message == abstention) == scenario.AbstentionExpected

	result.AnswerCorrect, result.AbstentionCorrect = &correct, &abstentionCorrect
	if !correct {
		result.Failures = append(result.Failures, Failure{Kind: "answer_incorrect"})
	}

	if !abstentionCorrect {
		result.Failures = append(result.Failures, Failure{Kind: "abstention_incorrect"})
	}

	if len(claims) > 0 {
		groundedness := float64(supported) / float64(len(claims))
		citationCorrectness := float64(cited) / float64(len(claims))
		result.Groundedness, result.CitationCorrectness = &groundedness, &citationCorrectness
	}

	if len(result.Failures) > 0 {
		result.Status = "failed"
	}

	return result
}

type claim struct{ text, citation string }

func parseClaims(message string, corpus []knowledge.Result) []claim {
	if message == abstention {
		return nil
	}

	message = strings.TrimSpace(strings.TrimPrefix(message, answerPrefix))

	var (
		claims []claim
		lines  []string
	)

	for _, line := range strings.Split(message, "\n") {
		if strings.HasPrefix(line, "Fonte:") {
			// A source label copied from the document is still part of its passage.
			candidate := strings.TrimSpace(strings.Join(lines, "\n") + "\n" + line)
			insideExcerpt := slices.ContainsFunc(corpus, func(row knowledge.Result) bool {
				return strings.HasPrefix(strings.TrimSpace(row.Text), candidate)
			})
			if insideExcerpt {
				lines = append(lines, line)
				continue
			}

			claims = append(claims, claim{strings.TrimSpace(strings.Join(lines, "\n")), line})
			lines = nil
		} else {
			lines = append(lines, line)
		}
	}

	if text := strings.TrimSpace(strings.Join(lines, "\n")); text != "" {
		claims = append(claims, claim{text: text})
	}

	return claims
}

func citation(row knowledge.Result) string {
	location := fmt.Sprintf("linhas %d–%d", row.StartLine, row.EndLine)
	if row.Page > 0 {
		location = fmt.Sprintf("página %d, %s", row.Page, location)
	}

	return fmt.Sprintf("Fonte: %s (documento %s, trecho %d, %s).",
		row.Source, row.DocumentID, row.Ordinal, location)
}

func checkCitation(claim claim, corpus []knowledge.Result, retrieved []string) string {
	if claim.citation == "" {
		return "citation_missing"
	}

	identity := citationIdentity.FindStringSubmatch(claim.citation)
	if identity == nil {
		return "citation_malformed"
	}

	id := identity[2] + ":" + identity[3]
	for _, row := range corpus {
		if evidenceID(row) != id || row.Source != identity[1] {
			continue
		}

		if !slices.Contains(retrieved, id) {
			return "citation_not_retrieved"
		}

		if claim.citation != citation(row) {
			return "citation_location_mismatch"
		}

		if claim.text != strings.TrimSpace(row.Text) {
			return "citation_no_support"
		}

		return ""
	}

	return "citation_nonexistent"
}
