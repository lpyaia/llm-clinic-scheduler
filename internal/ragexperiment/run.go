package ragexperiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/rageval"
)

// Run sequences the complete matrix. Every cell rebuilds a fresh index from the same immutable input.
// Embedding/retrieval failure or cancellation aborts without a partial report. Generation failures are
// recorded per scenario, and never count as abstention or approval. No application corpus is accessed.
func Run(ctx context.Context, dataset Dataset, matrix Matrix, options Options) (Report, error) {
	if err := dataset.validate(); err != nil {
		return Report{}, err
	}

	matrix, err := matrix.normalize()
	if err != nil {
		return Report{}, err
	}

	if options.Model == nil || options.Embedder == nil ||
		(options.Settings.Mode != "fixture" && options.Settings.Mode != "real") ||
		strings.TrimSpace(options.Settings.Generation.Provider) == "" ||
		strings.TrimSpace(options.Settings.Generation.Model) == "" {
		return Report{}, errors.New("invalid experiment providers")
	}

	if err := options.Settings.Embedding.Validate(); err != nil {
		return Report{}, err
	}

	datasetHash, err := fingerprint(dataset)
	if err != nil {
		return Report{}, err
	}

	matrixHash, err := fingerprint(matrix)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		DatasetSHA256: datasetHash,
		MatrixSHA256:  matrixHash,
		Dataset:       dataset,
		Matrix:        matrix,
		Settings:      options.Settings,
		Retrieval: "isolated in-memory exact cosine; metadata filters; " +
			"source/ordinal ties; normalized report UUIDs",
		Reranking: "none",
		Rubric:    "fixed-source-spans-v1; extractive-v1 grounding and citations",
		Limitations: "Fixture providers verify execution only. Real generation is probabilistic. " +
			"Answer correctness uses complete source-span coverage, not a general semantic judge.",
		Cells:       []Cell{},
		Comparisons: []Comparison{},
	}
	for _, chunking := range matrix.Chunking {
		for _, k := range matrix.TopK {
			cell, err := runCell(ctx, dataset, datasetHash, chunking, k, options)
			if err != nil {
				return Report{}, err
			}

			report.Cells = append(report.Cells, cell)
		}
	}

	for _, cell := range report.Cells[1:] {
		report.Comparisons = append(report.Comparisons, compare(report.Cells[0], cell))
	}

	return report, nil
}

func runCell(
	ctx context.Context, dataset Dataset, datasetHash string, chunking knowledge.Chunking, k int, options Options,
) (Cell, error) {
	if err := ctx.Err(); err != nil {
		return Cell{}, err
	}

	indexID, err := fingerprint(struct {
		Dataset  string
		Chunking knowledge.Chunking
		K        int
	}{datasetHash, chunking, k})
	if err != nil {
		return Cell{}, err
	}

	index := &vectorIndex{documents: make(map[string]indexedDocument)}

	service, err := knowledge.New(index, options.Embedder, options.Settings.Embedding, chunking)
	if err != nil {
		return Cell{}, err
	}

	for _, document := range dataset.Documents {
		_, err := service.Ingest(
			ctx,
			document.Source,
			strings.NewReader(strings.Join(document.Lines, "\n")),
			document.Metadata,
		)
		if err != nil {
			return Cell{}, fmt.Errorf("index %s ingest %s: %w", indexID, document.Source, err)
		}
	}

	cell := Cell{IndexID: indexID, Chunking: chunking, K: k, Index: index.snapshot(indexID), Scenarios: []Scenario{}}

	cell.IndexSHA256, err = fingerprint(cell.Index)
	if err != nil {
		return Cell{}, err
	}

	for _, question := range dataset.Questions {
		rows, err := service.Search(ctx, question.Text, k, question.Filter)
		if err != nil {
			return Cell{}, fmt.Errorf("index %s question %s: %w", indexID, question.ID, err)
		}

		for position := range rows {
			rows[position] = normalizeRow(rows[position], indexID)
		}

		item := scoreRetrieval(dataset, cell.Index, question, rows)
		model := &observedModel{delegate: options.Model}

		answer, err := agent.AnswerKnowledge(ctx, model, question.Text, rows)
		if ctx.Err() != nil {
			return Cell{}, ctx.Err()
		}

		item.Request, item.Response = model.request, model.response
		if err != nil {
			item.Status, item.Error, item.ErrorCode = "error", err.Error(), domain.ErrorCode(err)
			if item.ErrorCode == "MODEL_INVALID_RESPONSE" {
				item.Status = "failed"
				incorrect := false
				item.AnswerCorrect = &incorrect
			}
		} else {
			if model.response != nil {
				var selected struct {
					IDs []string `json:"excerpt_ids"`
				}
				if err := json.Unmarshal(model.response.Content, &selected); err != nil {
					return Cell{}, fmt.Errorf("capture validated answer selection: %w", err)
				}

				item.SelectedEvidence = selected.IDs
			}

			item.MessageLines = strings.Split(answer.Message, "\n")
			item.Assessment = rageval.AssessEvidence(indexResults(cell.Index), resultIDs(rows), answer.Message)
			scoreAnswer(dataset, cell.Index, &item)
		}

		cell.Scenarios = append(cell.Scenarios, item)
	}

	cell.Metrics = summarize(cell.Scenarios)

	return cell, nil
}

type observedModel struct {
	delegate agent.Model
	request  *agent.Request
	response *agent.Response
}

func (m *observedModel) Complete(ctx context.Context, request agent.Request) (agent.Response, error) {
	m.request = &request
	response, err := m.delegate.Complete(ctx, request)
	m.response = &response

	return response, err
}

func indexResults(index []Passage) []knowledge.Result {
	rows := make([]knowledge.Result, 0, len(index))
	for _, passage := range index {
		rows = append(rows, passage.Result)
	}

	return rows
}

func rowID(row knowledge.Result) string { return fmt.Sprintf("%s:%d", row.DocumentID, row.Ordinal) }

func resultIDs(rows []knowledge.Result) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, rowID(row))
	}

	return ids
}
