package retrievaleval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

func Load(reader io.Reader) (Dataset, error) {
	var dataset Dataset
	if err := Decode(reader, &dataset); err != nil {
		return dataset, err
	}

	return dataset, dataset.Validate()
}

// Decode reads one strict JSON value for datasets and controlled ranking fixtures.
func Decode(reader io.Reader, value any) error {
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

func (d Dataset) Validate() error {
	if d.Version != 1 || strings.TrimSpace(d.Corpus.ID) == "" ||
		len(d.Corpus.Documents) == 0 || len(d.Questions) == 0 {
		return errors.New("dataset requires version 1, an identified corpus, documents and questions")
	}

	sources := make(map[string]bool)

	for _, doc := range d.Corpus.Documents {
		ext := filepath.Ext(doc.Source)
		if strings.TrimSpace(doc.Source) != doc.Source || (ext != ".md" && ext != ".txt") ||
			sources[doc.Source] || strings.TrimSpace(doc.Content) == "" || !utf8.ValidString(doc.Content) ||
			strings.ContainsRune(doc.Content, 0) || len(doc.Content) > 64*1024 {
			return fmt.Errorf("invalid or duplicate corpus document %q (UTF-8 .md/.txt, maximum 64 KiB)", doc.Source)
		}

		if err := doc.Metadata.Validate(); err != nil {
			return fmt.Errorf("document %q: %w", doc.Source, err)
		}

		sources[doc.Source] = true
	}

	seen := make(map[string]bool)
	for _, question := range d.Questions {
		if strings.TrimSpace(question.ID) == "" || seen[question.ID] || strings.TrimSpace(question.Text) == "" ||
			!utf8.ValidString(question.Text) || utf8.RuneCountInString(question.Text) > 1000 ||
			question.NoAnswer != (len(question.Relevant) == 0) {
			return fmt.Errorf("invalid question %q: label relevant targets or explicitly set no_answer", question.ID)
		}

		if err := knowledge.Metadata(question.Filter).Validate(); err != nil {
			return fmt.Errorf("question %q: %w", question.ID, err)
		}

		for index, target := range question.Relevant {
			if !sources[target.Source] || target.Chunk < 0 {
				return fmt.Errorf("question %q: unknown source or invalid chunk: %+v", question.ID, target)
			}

			for _, previous := range question.Relevant[:index] {
				if previous.Source == target.Source &&
					(previous.Chunk == 0 || target.Chunk == 0 || previous.Chunk == target.Chunk) {
					return fmt.Errorf("question %q: duplicate or overlapping relevance labels", question.ID)
				}
			}
		}

		seen[question.ID] = true
	}

	return nil
}
