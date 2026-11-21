package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

func extractDocument(ctx context.Context, source string, input io.Reader, config Chunking) (string, []Chunk, error) {
	if strings.ToLower(filepath.Ext(source)) != ".pdf" {
		content, err := extract(source, input)
		if err != nil {
			return "", nil, err
		}

		return content, split(content, config), nil
	}

	return extractPDF(ctx, input, config)
}

// extractPDF handles text PDFs only. Chunk boundaries never cross a physical PDF page.
// Lines refer to extracted page text; page numbers refer to the original PDF, including blank pages.
func extractPDF(ctx context.Context, input io.Reader, config Chunking) (content string, chunks []Chunk, err error) {
	defer func() {
		if recover() != nil {
			content, chunks, err = "", nil, ErrExtraction
		}
	}()

	if input == nil {
		return "", nil, ErrExtraction
	}

	const maxPDFBytes = 1024 * 1024

	data, err := io.ReadAll(io.LimitReader(input, maxPDFBytes+1))
	if err != nil || len(data) > maxPDFBytes {
		return "", nil, ErrExtraction
	}

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", nil, fmt.Errorf("%w: invalid or encrypted PDF", ErrExtraction)
	}

	if reader.NumPage() < 1 || reader.NumPage() > 32 {
		return "", nil, ErrExtraction
	}

	var (
		pages      []string
		totalBytes int
	)

	for page := 1; page <= reader.NumPage(); page++ {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}

		text, err := reader.Page(page).GetPlainText(nil)
		if err != nil || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return "", nil, ErrExtraction
		}

		totalBytes += len(text)
		if totalBytes > 64*1024 {
			return "", nil, ErrExtraction
		}

		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		pages = append(pages, text)

		for _, chunk := range split(text, config) {
			chunk.Ordinal, chunk.Page = len(chunks)+1, page
			chunks = append(chunks, chunk)
		}
	}

	if len(chunks) == 0 {
		return "", nil, ErrEmpty
	}

	return strings.Join(pages, "\f"), chunks, nil
}
