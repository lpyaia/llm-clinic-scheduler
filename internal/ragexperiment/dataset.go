package ragexperiment

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/lpyaia/llm-clinic-scheduler/internal/retrievaleval"
)

func Load(reader io.Reader) (Dataset, error) {
	var dataset Dataset
	if err := retrievaleval.Decode(reader, &dataset); err != nil {
		return dataset, err
	}

	return dataset, dataset.validate()
}

func LoadMatrix(reader io.Reader) (Matrix, error) {
	var matrix Matrix
	if err := retrievaleval.Decode(reader, &matrix); err != nil {
		return matrix, err
	}

	return matrix.normalize()
}

func (d Dataset) validate() error {
	snapshot := retrievaleval.Dataset{
		Version: d.Version, Corpus: retrievaleval.Corpus{ID: d.CorpusID},
	}
	texts := make(map[string]string)

	for _, document := range d.Documents {
		for _, line := range document.Lines {
			if strings.ContainsAny(line, "\r\n") {
				return errors.New("document lines must not contain line breaks")
			}
		}

		text := strings.Join(document.Lines, "\n")
		if strings.HasPrefix(text, "\ufeff") {
			return errors.New("document lines must be normalized UTF-8 without BOM")
		}

		texts[document.Source] = text
		snapshot.Corpus.Documents = append(snapshot.Corpus.Documents, retrievaleval.Document{
			Source: document.Source, Content: text, Metadata: document.Metadata,
		})
	}

	for _, question := range d.Questions {
		if strings.TrimSpace(question.Reference) == "" || !utf8.ValidString(question.Reference) ||
			question.Expected == nil || question.NoAnswer != (len(question.Expected) == 0) {
			return fmt.Errorf("question %q requires reference and explicit evidence/abstention labels", question.ID)
		}

		item := retrievaleval.Question{
			ID:       question.ID,
			Text:     question.Text,
			Filter:   question.Filter,
			NoAnswer: question.NoAnswer,
		}
		sources := make(map[string]bool)

		seen := make(map[Evidence]bool)
		for _, evidence := range question.Expected {
			if strings.TrimSpace(evidence.Text) == "" || !utf8.ValidString(evidence.Text) || seen[evidence] ||
				strings.Count(texts[evidence.Source], evidence.Text) != 1 {
				return fmt.Errorf(
					"question %q evidence must identify a unique source passage: %+v",
					question.ID,
					evidence,
				)
			}

			seen[evidence] = true
			if !sources[evidence.Source] {
				item.Relevant = append(item.Relevant, retrievaleval.Target{Source: evidence.Source})
			}

			sources[evidence.Source] = true
		}

		snapshot.Questions = append(snapshot.Questions, item)
	}

	return snapshot.Validate()
}

func (m Matrix) normalize() (Matrix, error) {
	if len(m.Chunking) < 2 || len(m.Chunking) > 8 || len(m.TopK) < 2 || len(m.TopK) > 8 {
		return m, errors.New("matrix requires 2..8 chunkings and 2..8 distinct top-k values")
	}

	result := Matrix{TopK: append([]int(nil), m.TopK...)}
	seen := make(map[string]bool)
	strategies := make(map[string]bool)

	for _, chunking := range m.Chunking {
		config, err := chunking.Normalize()
		if err != nil {
			return m, err
		}

		key := fmt.Sprintf("%s:%d:%d", config.Strategy, config.MaxRunes, config.Overlap)
		if seen[key] {
			return m, errors.New("duplicate chunking configuration")
		}

		seen[key], strategies[config.Strategy] = true, true
		result.Chunking = append(result.Chunking, config)
	}

	if len(strategies) < 2 {
		return m, errors.New("matrix requires two distinct chunking strategies")
	}

	ks := make(map[int]bool)
	for _, k := range m.TopK {
		if k < 1 || k > 100 || ks[k] {
			return m, errors.New("top-k must be distinct values in 1..100")
		}

		ks[k] = true
	}

	return result, nil
}

func fingerprint(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}
