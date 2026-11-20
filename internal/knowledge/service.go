// Package knowledge ingests local clinic documents and retrieves citation-ready excerpts.
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrExtraction = errors.New(
		"document extraction failed; use UTF-8 .txt/.md or a text PDF within documented limits",
	)
	ErrEmpty         = errors.New("document contains no text")
	ErrEmbeddings    = errors.New("embedding generation failed")
	ErrConfiguration = errors.New("invalid embedding configuration")
	ErrIncompatible  = errors.New(
		"embedding configuration differs within the clinic corpus; rebuild the corpus with one configuration",
	)
	ErrInput = errors.New("invalid document, question or result limit (1..100)")
)

type EmbeddingConfig struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

func (c EmbeddingConfig) Validate() error {
	if c.Provider == "" || c.Model == "" || strings.TrimSpace(c.Provider) != c.Provider ||
		strings.TrimSpace(c.Model) != c.Model || c.Dimensions < 1 || c.Dimensions > 16000 {
		return ErrConfiguration
	}

	return nil
}

// ValidateVector rejects incompatible, non-finite and zero vectors before persistence or comparison.
func (c EmbeddingConfig) ValidateVector(vector []float32) error {
	if err := c.Validate(); err != nil {
		return err
	}

	if len(vector) != c.Dimensions {
		return fmt.Errorf("%w: expected %d dimensions, received %d", ErrEmbeddings, c.Dimensions, len(vector))
	}

	var norm float64

	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("%w: non-finite vector", ErrEmbeddings)
		}

		norm += float64(value) * float64(value)
	}

	if norm == 0 {
		return fmt.Errorf("%w: zero vector has no cosine similarity", ErrEmbeddings)
	}

	return nil
}

type Document struct {
	ID              uuid.UUID       `json:"id"`
	Source          string          `json:"source"`
	Content         string          `json:"-"`
	EmbeddingConfig EmbeddingConfig `json:"embedding_config"`
	ChunkCount      int             `json:"chunk_count"`
	Metadata        Metadata        `json:"metadata"`
}

type Chunk struct {
	Ordinal            int
	Text               string
	StartLine, EndLine int
	Vector             []float32
	Page               int
	// Rune offsets are half-open within normalized document text, or within a PDF page.
	StartRune, EndRune int
}

type Result struct {
	DocumentID uuid.UUID `json:"document_id"`
	Source     string    `json:"source"`
	Ordinal    int       `json:"chunk"`
	Text       string    `json:"text"`
	StartLine  int       `json:"start_line"`
	EndLine    int       `json:"end_line"`
	Similarity float64   `json:"similarity"`
	Metadata   Metadata  `json:"metadata"       gorm:"embedded"`
	Page       int       `json:"page,omitempty"`
}

// Embedder is the external embedding-provider boundary. Outputs correspond to inputs in order.
type Embedder interface {
	Embed(context.Context, EmbeddingConfig, []string) ([][]float32, error)
}

// Repository replaces the current document and all chunks with the same source atomically, without history.
// Replacement must serialize with other imports; failures preserve the previous version.
// CorpusConfig returns nil for an empty corpus
// and ErrIncompatible for mixed configurations. SearchCorpus must reject incompatible configurations
// before comparing vectors. SaveDocument serializes the configuration check with insertion, so
// concurrent imports cannot establish different configurations. Failed writes leave no partial state.
type Repository interface {
	SaveDocument(context.Context, Document, []Chunk) error
	CorpusConfig(context.Context) (*EmbeddingConfig, error)
	// Scopes returns distinct current metadata for resolving names without loading document content.
	Scopes(context.Context) ([]Metadata, error)
	SearchCorpus(context.Context, EmbeddingConfig, []float32, int, ResolvedFilter) ([]Result, error)
}

type Service struct {
	repo     Repository
	embedder Embedder
	config   EmbeddingConfig
	chunking Chunking
}

// New keeps the default paragraph policy unless one chunking configuration is supplied.
// Custom policies require a separately rebuilt corpus; chunking is not persisted as embedding configuration.
func New(repo Repository, embedder Embedder, config EmbeddingConfig, chunking ...Chunking) (*Service, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if repo == nil || embedder == nil {
		return nil, ErrConfiguration
	}

	if len(chunking) > 1 {
		return nil, ErrInput
	}

	var selected Chunking
	if len(chunking) == 1 {
		selected = chunking[0]
	}

	selected, err := selected.Normalize()
	if err != nil {
		return nil, err
	}

	return &Service{repo: repo, embedder: embedder, config: config, chunking: selected}, nil
}

// Ingest accepts optional metadata for compatibility with existing unclassified documents.
func (s *Service) Ingest(ctx context.Context, source string, input io.Reader, metadata ...Metadata) (Document, error) {
	if len(metadata) > 1 {
		return Document{}, ErrInput
	}

	meta := Metadata{Topic: "general"}
	if len(metadata) == 1 {
		meta = metadata[0]
	}

	if err := meta.Validate(); err != nil {
		return Document{}, err
	}

	if meta.Topic == "" {
		meta.Topic = "general"
	}

	content, chunks, err := extractDocument(ctx, source, input, s.chunking)
	if err != nil {
		return Document{}, err
	}

	if len(chunks) > 256 {
		return Document{}, fmt.Errorf("%w: maximum 256 chunks", ErrExtraction)
	}

	if _, err := s.validateCorpus(ctx); err != nil {
		return Document{}, err
	}

	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = chunk.Text
	}

	vectors, err := s.embed(ctx, texts)
	if err != nil {
		return Document{}, err
	}

	for i := range chunks {
		chunks[i].Vector = vectors[i]
	}

	doc := Document{
		ID: uuid.New(), Source: source, Content: content,
		EmbeddingConfig: s.config, ChunkCount: len(chunks),
		Metadata: meta,
	}
	if err := s.repo.SaveDocument(ctx, doc, chunks); err != nil {
		return Document{}, fmt.Errorf("save document: %w", err)
	}

	return doc, nil
}

// Search accepts one optional filter; omitted filters retain the corpus-wide CLI behavior.
func (s *Service) Search(ctx context.Context, question string, limit int, filters ...Filter) ([]Result, error) {
	if len(filters) > 1 {
		return nil, ErrInput
	}

	var filter Filter
	if len(filters) == 1 {
		filter = filters[0]
	}

	if err := Metadata(filter).Validate(); err != nil {
		return nil, err
	}

	question = strings.TrimSpace(question)
	if question == "" || !utf8.ValidString(question) || utf8.RuneCountInString(question) > 1000 ||
		limit < 1 || limit > 100 {
		return nil, ErrInput
	}

	hasDocuments, err := s.validateCorpus(ctx)
	if err != nil {
		return nil, err
	}

	if !hasDocuments {
		return []Result{}, nil
	}

	filter, matched, err := s.resolveFilter(ctx, filter)
	if err != nil {
		return nil, err
	}

	if !matched {
		return []Result{}, nil
	}

	vectors, err := s.embed(ctx, []string{question})
	if err != nil {
		return nil, err
	}

	resolved := ResolvedFilter{Filter: filter, GeneralOnly: filter.generalPolicy()}

	return s.repo.SearchCorpus(ctx, s.config, vectors[0], limit, resolved)
}

// validateCorpus returns whether documents exist and rejects any incompatible corpus configuration.
func (s *Service) validateCorpus(ctx context.Context) (bool, error) {
	cfg, err := s.repo.CorpusConfig(ctx)
	if err != nil {
		return false, err
	}

	if cfg == nil {
		return false, nil
	}

	if *cfg != s.config {
		return false, ErrIncompatible
	}

	return true, nil
}

func (s *Service) embed(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := s.embedder.Embed(ctx, s.config, texts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEmbeddings, err)
	}

	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("%w: output count differs from input count", ErrEmbeddings)
	}

	for _, vector := range vectors {
		if err := s.config.ValidateVector(vector); err != nil {
			return nil, err
		}
	}

	return vectors, nil
}

func extract(source string, input io.Reader) (string, error) {
	ext := strings.ToLower(filepath.Ext(source))
	if strings.TrimSpace(source) == "" || (ext != ".txt" && ext != ".md") || input == nil {
		return "", ErrExtraction
	}

	const maxBytes = 64 * 1024

	data, err := io.ReadAll(io.LimitReader(input, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrExtraction, err)
	}

	if len(data) > maxBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return "", ErrExtraction
	}

	content := strings.TrimPrefix(string(data), "\ufeff")

	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if strings.TrimSpace(content) == "" {
		return "", ErrEmpty
	}

	return content, nil
}
