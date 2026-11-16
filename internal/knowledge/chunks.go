package knowledge

import "strings"

// split keeps paragraphs together up to 1000 runes. Long lines retain all nonblank text;
// overlapping line ranges are intentional when two chunks come from the same source line.
func split(content string) []Chunk {
	const maxRunes = 1000

	var (
		chunks     []Chunk
		text       []rune
		start, end int
	)

	flush := func() {
		if strings.TrimSpace(string(text)) == "" {
			text = nil

			return
		}

		chunks = append(chunks, Chunk{
			Ordinal: len(chunks) + 1, Text: string(text), StartLine: start, EndLine: end,
		})
		text = nil
	}

	for i, line := range strings.Split(content, "\n") {
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

			flush()

			runes = runes[maxRunes:]
		}

		if len(runes) == 0 {
			continue
		}

		if len(text) == 0 {
			start = i + 1
		} else {
			text = append(text, '\n')
		}

		text = append(text, runes...)
		end = i + 1
	}

	flush()

	return chunks
}
