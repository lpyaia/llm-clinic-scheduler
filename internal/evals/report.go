package evals

import (
	"encoding/json"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type Configuration struct {
	Mode     string          `json:"mode"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Timeout  string          `json:"timeout,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

type Report struct {
	DatasetSHA256 string           `json:"dataset_sha256"`
	Configuration Configuration    `json:"configuration"`
	Reproduction  string           `json:"reproduction"`
	Source        *Configuration   `json:"source_configuration,omitempty"`
	Scenarios     []ScenarioReport `json:"scenarios"`
	Totals        Totals           `json:"totals"`
}

type Totals struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	Errors int `json:"errors"`
}

type ScenarioReport struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Turns  []TurnReport `json:"turns"`
}

type TurnReport struct {
	Message      string               `json:"message"`
	Now          time.Time            `json:"now"`
	Expected     Expectations         `json:"expected"`
	Result       agent.Result         `json:"result"`
	Appointments []domain.Appointment `json:"appointments"`
	Calls        []Call               `json:"calls"`
	Effects      []string             `json:"effects"`
	Failures     []string             `json:"failures"`
	Error        string               `json:"error,omitempty"`
	ErrorCode    string               `json:"error_code,omitempty"`
}

// Request includes the full prompt, tool schemas and structured-answer configuration.
type Call struct {
	Request  agent.Request `json:"request"`
	Response Completion    `json:"response"`
}
