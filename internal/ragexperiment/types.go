// Package ragexperiment compares isolated RAG configurations against a fixed source snapshot.
package ragexperiment

import (
	"encoding/json"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/rageval"
)

type Dataset struct {
	Version   int        `json:"version"`
	CorpusID  string     `json:"corpus_id"`
	Documents []Document `json:"documents"`
	Questions []Question `json:"questions"`
}

type Document struct {
	Source   string             `json:"source"`
	Lines    []string           `json:"lines"`
	Metadata knowledge.Metadata `json:"metadata"`
}

type Question struct {
	ID        string           `json:"id"`
	Text      string           `json:"text"`
	Reference string           `json:"reference"`
	Filter    knowledge.Filter `json:"filter"`
	NoAnswer  bool             `json:"no_answer"`
	Expected  []Evidence       `json:"expected"`
}

// Evidence is a unique literal source passage, independent of chunk ordinals and sizes.
type Evidence struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}

type Matrix struct {
	Chunking []knowledge.Chunking `json:"chunking"`
	TopK     []int                `json:"top_k"`
}

type Generation struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

type Settings struct {
	Mode         string                    `json:"mode"`
	Embedding    knowledge.EmbeddingConfig `json:"embedding"`
	Generation   Generation                `json:"generation"`
	CodeRevision string                    `json:"code_revision"`
	Dirty        bool                      `json:"dirty"`
	Timeout      string                    `json:"timeout"`
}

// Options varies only the external embedding and generation providers.
type Options struct {
	Embedder knowledge.Embedder
	Model    agent.Model
	Settings Settings
}

type Report struct {
	DatasetSHA256 string       `json:"dataset_sha256"`
	MatrixSHA256  string       `json:"matrix_sha256"`
	Dataset       Dataset      `json:"dataset"`
	Matrix        Matrix       `json:"matrix"`
	Settings      Settings     `json:"settings"`
	Retrieval     string       `json:"retrieval"`
	Reranking     string       `json:"reranking"`
	Rubric        string       `json:"rubric"`
	Limitations   string       `json:"limitations"`
	Cells         []Cell       `json:"cells"`
	Comparisons   []Comparison `json:"comparisons"`
}

type Passage struct {
	knowledge.Result
	StartRune int `json:"start_rune"`
	EndRune   int `json:"end_rune"`
}

type Cell struct {
	IndexID     string             `json:"index_id"`
	IndexSHA256 string             `json:"index_sha256"`
	Chunking    knowledge.Chunking `json:"chunking"`
	K           int                `json:"k"`
	Index       []Passage          `json:"index"`
	Scenarios   []Scenario         `json:"scenarios"`
	Metrics     Metrics            `json:"metrics"`
}

type Scenario struct {
	Question          Question                   `json:"question"`
	Status            string                     `json:"status"`
	Retrieved         []knowledge.Result         `json:"retrieved"`
	SelectedEvidence  []string                   `json:"selected_evidence"`
	Recall            *float64                   `json:"recall"`
	Missing           []Evidence                 `json:"missing"`
	Request           *agent.Request             `json:"request,omitempty"`
	Response          *agent.Response            `json:"response,omitempty"`
	MessageLines      []string                   `json:"message_lines"`
	Assessment        rageval.EvidenceAssessment `json:"assessment"`
	AnswerCorrect     *bool                      `json:"answer_correct"`
	AbstentionCorrect *bool                      `json:"abstention_correct"`
	Error             string                     `json:"error,omitempty"`
	ErrorCode         string                     `json:"error_code,omitempty"`
}

// Metric is a scenario-weighted mean. Count identifies its exact non-null denominator.
type Metric struct {
	Value *float64 `json:"value"`
	Count int      `json:"count"`
}

type Metrics struct {
	Recall              Metric `json:"recall"`
	Groundedness        Metric `json:"groundedness"`
	AnswerCorrect       Metric `json:"answer_correct"`
	CitationCorrectness Metric `json:"citation_correctness"`
	AbstentionCorrect   Metric `json:"abstention_correct"`
	UnsupportedClaims   int    `json:"unsupported_claims"`
	Passed              int    `json:"passed"`
	Failed              int    `json:"failed"`
	Errors              int    `json:"errors"`
}

// Comparison contains paired scenario deltas; null metrics are never imputed as zero.
type Comparison struct {
	BaselineID string  `json:"baseline_id"`
	IndexID    string  `json:"index_id"`
	Deltas     Metrics `json:"deltas"`
}
