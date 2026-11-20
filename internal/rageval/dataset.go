// Package rageval evaluates extractive RAG answers against manually labelled evidence.
package rageval

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

type Dataset struct {
	Version   int                `json:"version"`
	Corpus    []knowledge.Result `json:"corpus"`
	Scenarios []Scenario         `json:"scenarios"`
}

type Scenario struct {
	ID                 string   `json:"id"`
	Question           string   `json:"question"`
	ReferenceAnswer    string   `json:"reference_answer"`
	ExpectedEvidence   []string `json:"expected_evidence"`
	AbstentionExpected bool     `json:"abstention_expected"`
}

// Run stores observed output separately from the manual reference labels.
// Retrieved identifies the final retrieval of this turn, never conversation history.
type Run struct {
	DatasetSHA256 string                 `json:"dataset_sha256"`
	Provider      string                 `json:"provider"`
	Model         string                 `json:"model"`
	Responses     map[string]Observation `json:"responses"`
}

type Observation struct {
	Retrieved    []string `json:"retrieved"`
	MessageLines []string `json:"message_lines"`
	Error        string   `json:"error,omitempty"`
}

func (d Dataset) Fingerprint() (string, error) {
	encoded, err := json.Marshal(d)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func evidenceID(row knowledge.Result) string {
	return fmt.Sprintf("%s:%d", row.DocumentID, row.Ordinal)
}

func Load(reader io.Reader) (Dataset, error) {
	var dataset Dataset
	if err := decode(reader, &dataset); err != nil {
		return dataset, err
	}

	return dataset, dataset.validate()
}

func LoadRun(reader io.Reader) (Run, error) {
	var run Run

	err := decode(reader, &run)

	return run, err
}

func decode(reader io.Reader, value any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		return err
	}

	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("expected exactly one JSON value")
	}

	return nil
}

func (d Dataset) validate() error {
	if d.Version != 1 || len(d.Corpus) == 0 || len(d.Scenarios) == 0 {
		return errors.New("RAG dataset requires version 1, a corpus and scenarios")
	}

	corpus := make(map[string]bool)
	documents := make(map[uuid.UUID]string)
	sources := make(map[string]uuid.UUID)

	for _, row := range d.Corpus {
		id := evidenceID(row)
		if row.DocumentID == uuid.Nil || !nonblank(row.Source) || !nonblank(row.Text) ||
			row.Ordinal < 1 || row.StartLine < 1 || row.EndLine < row.StartLine || row.Page < 0 || corpus[id] ||
			strings.ContainsAny(row.Source, "\r\n") || strings.Contains(row.Text, "\r") ||
			math.IsNaN(row.Similarity) || math.IsInf(row.Similarity, 0) {
			return fmt.Errorf("invalid or duplicate evidence %q", id)
		}

		if source, ok := documents[row.DocumentID]; ok && source != row.Source {
			return fmt.Errorf("document %s has inconsistent sources", row.DocumentID)
		}

		if document, ok := sources[row.Source]; ok && document != row.DocumentID {
			return fmt.Errorf("source %q has multiple document versions", row.Source)
		}

		if err := row.Metadata.Validate(); err != nil {
			return fmt.Errorf("evidence %q: %w", id, err)
		}

		corpus[id], documents[row.DocumentID], sources[row.Source] = true, row.Source, row.DocumentID
	}

	seen := make(map[string]bool)
	for _, scenario := range d.Scenarios {
		if !nonblank(scenario.ID) || seen[scenario.ID] || !nonblank(scenario.Question) ||
			!nonblank(scenario.ReferenceAnswer) || scenario.ExpectedEvidence == nil ||
			scenario.AbstentionExpected != (len(scenario.ExpectedEvidence) == 0) {
			return fmt.Errorf(
				"scenario %q needs unique identity, question, reference and explicit evidence labels",
				scenario.ID,
			)
		}

		if err := validateIDs(scenario.ExpectedEvidence, corpus); err != nil {
			return fmt.Errorf("scenario %q: %w", scenario.ID, err)
		}

		seen[scenario.ID] = true
	}

	return nil
}

func validateRun(dataset Dataset, run Run) error {
	hash, err := dataset.Fingerprint()
	if err != nil {
		return err
	}

	if run.DatasetSHA256 != hash || !nonblank(run.Provider) || !nonblank(run.Model) ||
		len(run.Responses) != len(dataset.Scenarios) {
		return errors.New(
			"run requires the current dataset fingerprint, provider/model and exactly one response per scenario",
		)
	}

	corpus := make(map[string]bool)
	for _, row := range dataset.Corpus {
		corpus[evidenceID(row)] = true
	}

	for _, scenario := range dataset.Scenarios {
		observation, ok := run.Responses[scenario.ID]
		if !ok || (observation.Error == "" && (observation.MessageLines == nil || observation.Retrieved == nil)) {
			return fmt.Errorf(
				"scenario %q is missing a response or explicit retrieved/message_lines arrays",
				scenario.ID,
			)
		}

		if err := validateIDs(observation.Retrieved, corpus); err != nil {
			return fmt.Errorf("scenario %q retrieval: %w", scenario.ID, err)
		}

		for _, line := range observation.MessageLines {
			if !utf8.ValidString(line) || strings.ContainsAny(line, "\r\n") {
				return fmt.Errorf(
					"scenario %q: message_lines must contain UTF-8 lines without line breaks",
					scenario.ID,
				)
			}
		}
	}

	return nil
}

func validateIDs(ids []string, corpus map[string]bool) error {
	seen := make(map[string]bool)
	for _, id := range ids {
		if !corpus[id] || seen[id] {
			return fmt.Errorf("unknown or duplicate evidence %q", id)
		}

		seen[id] = true
	}

	return nil
}

func nonblank(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value)
}
