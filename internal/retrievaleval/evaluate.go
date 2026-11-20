// Package retrievaleval measures retrieval against manually labelled, controlled clinic materials.
package retrievaleval

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

type Dataset struct {
	Version   int        `json:"version"`
	Corpus    Corpus     `json:"corpus"`
	Questions []Question `json:"questions"`
}

type Corpus struct {
	ID        string     `json:"id"`
	Documents []Document `json:"documents"`
}

type Document struct {
	Source   string             `json:"source"`
	Content  string             `json:"content"`
	Metadata knowledge.Metadata `json:"metadata"`
}

// Target identifies a document, or exactly one chunk when Chunk is positive. Ordinals start at one.
type Target struct {
	Source string `json:"source"`
	Chunk  int    `json:"chunk,omitempty"`
}

type Question struct {
	ID       string           `json:"id"`
	Text     string           `json:"text"`
	Filter   knowledge.Filter `json:"filter"`
	NoAnswer bool             `json:"no_answer"`
	Relevant []Target         `json:"relevant"`
}

type Configuration struct {
	Mode         string                    `json:"mode"`
	K            int                       `json:"k"`
	Embedding    knowledge.EmbeddingConfig `json:"embedding"`
	Retrieval    string                    `json:"retrieval"`
	Chunking     string                    `json:"chunking"`
	CodeRevision string                    `json:"code_revision"`
	Dirty        bool                      `json:"dirty"`
	Timeout      string                    `json:"timeout"`
}

type Report struct {
	DatasetSHA256 string           `json:"dataset_sha256"`
	CorpusSHA256  string           `json:"corpus_sha256"`
	Corpus        Corpus           `json:"corpus"`
	Configuration Configuration    `json:"configuration"`
	Questions     []QuestionReport `json:"questions"`
	Totals        Totals           `json:"totals"`
}

type QuestionReport struct {
	Question
	Recall  *float64       `json:"recall_at_k"`
	Missing []Target       `json:"missing"`
	Results []RankedResult `json:"results"`
}

type RankedResult struct {
	knowledge.Result
	Rank     int  `json:"rank"`
	Relevant bool `json:"relevant"`
}

type Totals struct {
	Answerable          int      `json:"answerable"`
	NoAnswer            int      `json:"no_answer"`
	NoAnswerWithResults int      `json:"no_answer_with_results"`
	MeanRecall          *float64 `json:"mean_recall_at_k"`
	WithMissing         []string `json:"with_missing"`
	WithIrrelevant      []string `json:"with_irrelevant"`
}

// Evaluate scores the first k results in retrieval order, counting each relevant target at most once.
func Evaluate(dataset Dataset, rankings map[string][]knowledge.Result, config Configuration) (Report, error) {
	if err := dataset.Validate(); err != nil {
		return Report{}, err
	}

	if config.K < 1 || config.K > 100 || (config.Mode != "fixture" && config.Mode != "real") {
		return Report{}, errors.New("evaluation requires k in 1..100 and mode fixture or real")
	}

	if err := validateRankings(dataset, rankings); err != nil {
		return Report{}, err
	}

	datasetHash, err := dataset.Fingerprint()
	if err != nil {
		return Report{}, err
	}

	corpusHash, err := fingerprint(dataset.Corpus)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		Configuration: config, DatasetSHA256: datasetHash, CorpusSHA256: corpusHash, Corpus: dataset.Corpus,
		Questions: []QuestionReport{},
		Totals:    Totals{WithMissing: []string{}, WithIrrelevant: []string{}},
	}

	var sum float64

	for _, question := range dataset.Questions {
		item := QuestionReport{Question: question, Missing: []Target{}, Results: []RankedResult{}}
		found := make(map[Target]bool)
		irrelevant := false

		for index, result := range rankings[question.ID] {
			if index >= config.K {
				break
			}

			relevant := false

			for _, target := range question.Relevant {
				if target.Source == result.Source && (target.Chunk == 0 || target.Chunk == result.Ordinal) {
					found[target], relevant = true, true
				}
			}

			item.Results = append(item.Results, RankedResult{Result: result, Rank: index + 1, Relevant: relevant})
			irrelevant = irrelevant || !relevant
		}

		for _, target := range question.Relevant {
			if !found[target] {
				item.Missing = append(item.Missing, target)
			}
		}

		if question.NoAnswer {
			report.Totals.NoAnswer++
			if len(item.Results) > 0 {
				report.Totals.NoAnswerWithResults++
			}
		} else {
			recall := float64(len(found)) / float64(len(question.Relevant))
			item.Recall = &recall
			sum += recall
			report.Totals.Answerable++
		}

		if len(item.Missing) > 0 {
			report.Totals.WithMissing = append(report.Totals.WithMissing, question.ID)
		}

		if irrelevant {
			report.Totals.WithIrrelevant = append(report.Totals.WithIrrelevant, question.ID)
		}

		report.Questions = append(report.Questions, item)
	}

	if report.Totals.Answerable > 0 {
		mean := sum / float64(report.Totals.Answerable)
		report.Totals.MeanRecall = &mean
	}

	return report, nil
}

func (d Dataset) Fingerprint() (string, error) { return fingerprint(d) }

func fingerprint(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func validateRankings(dataset Dataset, rankings map[string][]knowledge.Result) error {
	if len(rankings) != len(dataset.Questions) {
		return errors.New("rankings must contain exactly one entry per question, including empty results")
	}

	sources := make(map[string]bool)
	for _, doc := range dataset.Corpus.Documents {
		sources[doc.Source] = true
	}

	for _, question := range dataset.Questions {
		results, exists := rankings[question.ID]
		if !exists {
			return fmt.Errorf("missing ranking for question %q", question.ID)
		}

		seen := make(map[Target]bool)

		for _, result := range results {
			target := Target{Source: result.Source, Chunk: result.Ordinal}
			if !sources[result.Source] || result.Ordinal < 1 || seen[target] ||
				math.IsNaN(result.Similarity) || math.IsInf(result.Similarity, 0) {
				return fmt.Errorf("question %q: invalid or duplicate retrieval result %+v", question.ID, target)
			}

			seen[target] = true
		}
	}

	return nil
}
