package trace

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

const redacted = "[redacted]"

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbearer\s+[^\s,;"\\]+`),
	regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]+`),
	regexp.MustCompile(`[a-fA-F0-9]{64}`),
	regexp.MustCompile(`(?i)(?:password|secret|api[_-]?key|access[_-]?token)\s*[:=]\s*[^\s,;"\\]+`),
}

type redactor struct {
	secrets *strings.Replacer
}

func newRedactor(secrets []string) *redactor {
	values := append([]string(nil), secrets...)
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })

	var pairs []string

	for _, value := range values {
		if value != "" {
			pairs = append(pairs, value, redacted)
		}
	}

	return &redactor{secrets: strings.NewReplacer(pairs...)}
}

func (r *redactor) text(value string) string {
	value = r.secrets.Replace(value)
	for _, pattern := range credentialPatterns {
		value = pattern.ReplaceAllString(value, redacted)
	}

	return value
}

func (r *redactor) content(raw []byte) json.RawMessage {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return json.RawMessage(`"[unavailable]"`)
	}

	cleaned, err := json.Marshal(r.clean(value, 0))
	if err != nil {
		return json.RawMessage(`"[unavailable]"`)
	}

	return cleaned
}

func (r *redactor) clean(value any, depth int) any {
	if depth > 32 {
		return redacted
	}

	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			cleanKey := r.text(key)
			if sensitiveKey(key) {
				out[cleanKey] = redacted
			} else {
				out[cleanKey] = r.clean(child, depth+1)
			}
		}

		return out
	case []any:
		for i := range v {
			v[i] = r.clean(v[i], depth+1)
		}

		return v
	case string:
		cleanedText := r.text(v)
		if cleanedText == redacted {
			return cleanedText
		}

		// Tool arguments and message bodies can themselves contain encoded JSON.
		var nested any

		decoder := json.NewDecoder(strings.NewReader(v))
		decoder.UseNumber()

		if json.Valid([]byte(v)) && decoder.Decode(&nested) == nil {
			cleaned, err := json.Marshal(r.clean(nested, depth+1))
			if err == nil {
				return string(cleaned)
			}
		}

		return cleanedText
	default:
		// Numeric, boolean and null JSON values can also spell a configured secret.
		raw, err := json.Marshal(v)
		if err == nil && r.text(string(raw)) != string(raw) {
			return redacted
		}

		return v
	}
}

func sensitiveKey(key string) bool {
	key = strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(key))
	for _, part := range []string{"authorization", "password", "secret", "token", "apikey", "cookie", "credential"} {
		if strings.Contains(key, part) {
			return true
		}
	}

	return false
}
