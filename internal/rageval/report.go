package rageval

import "github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"

type Report struct {
	DatasetSHA256 string             `json:"dataset_sha256"`
	Provider      string             `json:"provider"`
	Model         string             `json:"model"`
	Rubric        string             `json:"rubric"`
	Limitations   string             `json:"limitations"`
	Corpus        []knowledge.Result `json:"corpus"`
	Scenarios     []ScenarioReport   `json:"scenarios"`
	Totals        Totals             `json:"totals"`
}

type ScenarioReport struct {
	Scenario
	Observation
	Status              string    `json:"status"`
	Groundedness        *float64  `json:"groundedness"`
	AnswerCorrect       *bool     `json:"answer_correct"`
	CitationCorrectness *float64  `json:"citation_correctness"`
	AbstentionCorrect   *bool     `json:"abstention_correct"`
	UnsupportedClaims   []string  `json:"unsupported_claims"`
	Failures            []Failure `json:"failures"`
}

type Failure struct {
	Kind     string `json:"kind"`
	Claim    string `json:"claim,omitempty"`
	Citation string `json:"citation,omitempty"`
}

type Totals struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	Errors int `json:"errors"`
}
