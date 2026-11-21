package knowledge

import (
	"errors"
	"strings"
)

// Chunking selects paragraph-preserving chunks or sliding rune windows.
// Zero values retain the production default. Overlap is supported only by windows.
type Chunking struct {
	Strategy string `json:"strategy"`
	MaxRunes int    `json:"max_runes"`
	Overlap  int    `json:"overlap"`
}

func (c Chunking) Normalize() (Chunking, error) {
	if c.Strategy == "" {
		c.Strategy = "paragraphs"
	}

	if c.MaxRunes == 0 {
		c.MaxRunes = 1000
	}

	if (c.Strategy != "paragraphs" && c.Strategy != "windows") || c.MaxRunes < 1 || c.MaxRunes > 1000 ||
		c.Overlap < 0 || c.Overlap >= c.MaxRunes || (c.Strategy == "paragraphs" && c.Overlap != 0) {
		return c, errors.New("invalid chunking: paragraphs or windows, maximum 1..1000 runes, overlap below maximum")
	}

	return c, nil
}

// split keeps paragraphs together up to 1000 runes. Long lines retain all nonblank text;
// overlapping line ranges are intentional when two chunks come from the same source line.
func split(content string, config Chunking) []Chunk {
	if config.Strategy == "windows" {
		return windows(content, config)
	}

	maxRunes := config.MaxRunes

	var (
		chunks               []Chunk
		text                 []rune
		start, end           int
		position, chunkStart int
	)

	flush := func() {
		if strings.TrimSpace(string(text)) == "" {
			text = nil

			return
		}

		chunks = append(chunks, Chunk{
			Ordinal: len(chunks) + 1, Text: string(text), StartLine: start, EndLine: end,
			StartRune: chunkStart, EndRune: chunkStart + len(text),
		})
		text = nil
	}

	for i, line := range strings.Split(content, "\n") {
		lineStart := position
		position += len([]rune(line)) + 1

		if strings.TrimSpace(line) == "" {
			flush()

			continue
		}

		runes := []rune(line)
		if len(text)+1+len(runes) > maxRunes {
			flush()
		}

		for len(runes) > maxRunes {
			text, start, end = runes[:maxRunes], i+1, i+1
			chunkStart = lineStart

			flush()

			runes = runes[maxRunes:]
			lineStart += maxRunes
		}

		if len(runes) == 0 {
			continue
		}

		if len(text) == 0 {
			start = i + 1
			chunkStart = lineStart
		} else {
			text = append(text, '\n')
		}

		text = append(text, runes...)
		end = i + 1
	}

	flush()

	return chunks
}

func windows(content string, config Chunking) []Chunk {
	runes := []rune(content)

	lines := make([]int, len(runes))
	line := 1
	for position, char := range runes {
		lines[position] = line
		if char == '\n' {
			line++
		}
	}

	var chunks []Chunk

	for start := 0; start < len(runes); start += config.MaxRunes - config.Overlap {
		end := min(start+config.MaxRunes, len(runes))
		text := string(runes[start:end])
		if strings.TrimSpace(text) != "" {
			chunks = append(chunks, Chunk{
				Ordinal: len(chunks) + 1, Text: text, StartRune: start, EndRune: end,
				StartLine: lines[start], EndLine: lines[end-1],
			})
		}

		if end == len(runes) {
			break
		}
	}

	return chunks
}
