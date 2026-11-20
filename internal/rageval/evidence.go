package rageval

import (
	"slices"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

// EvidenceAssessment scores claims independently of question-specific reference labels.
// Corpus and retrieved IDs must describe one validated snapshot. Unknown IDs never provide support.
type EvidenceAssessment struct {
	Groundedness        *float64          `json:"groundedness"`
	CitationCorrectness *float64          `json:"citation_correctness"`
	Abstained           bool              `json:"abstained"`
	Claims              []ClaimAssessment `json:"claims"`
	UnsupportedClaims   []string          `json:"unsupported_claims"`
	Failures            []Failure         `json:"failures"`
}

type ClaimAssessment struct {
	Text     string   `json:"text"`
	Citation string   `json:"citation,omitempty"`
	Evidence []string `json:"evidence"`
}

// AssessEvidence shares the extractive grounding/citation rubric with experiments whose
// reference facts remain fixed while chunk boundaries change. It performs no model calls.
func AssessEvidence(corpus []knowledge.Result, retrieved []string, message string) EvidenceAssessment {
	message = strings.TrimSpace(message)
	result := EvidenceAssessment{
		Abstained: message == abstention, Claims: []ClaimAssessment{},
		UnsupportedClaims: []string{}, Failures: []Failure{},
	}
	claims := parseClaims(message, corpus)
	supported, cited := 0, 0

	for _, claim := range claims {
		item := ClaimAssessment{Text: claim.text, Citation: claim.citation, Evidence: []string{}}
		isSupported := false

		for _, row := range corpus {
			if claim.text == strings.TrimSpace(row.Text) {
				item.Evidence = append(item.Evidence, evidenceID(row))
				isSupported = isSupported || slices.Contains(retrieved, evidenceID(row))
			}
		}

		result.Claims = append(result.Claims, item)

		if isSupported {
			supported++
		} else {
			result.UnsupportedClaims = append(result.UnsupportedClaims, claim.text)
			result.Failures = append(result.Failures, Failure{Kind: "unsupported_claim", Claim: claim.text})
		}

		if failure := checkCitation(claim, corpus, retrieved); failure != "" {
			result.Failures = append(
				result.Failures,
				Failure{Kind: failure, Claim: claim.text, Citation: claim.citation},
			)
		} else {
			cited++
		}
	}

	if len(claims) > 0 {
		grounded, citations := float64(supported)/float64(len(claims)), float64(cited)/float64(len(claims))
		result.Groundedness, result.CitationCorrectness = &grounded, &citations
	}

	return result
}
