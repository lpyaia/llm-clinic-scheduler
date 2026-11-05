package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"

	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// DecodeObject accepts exactly one object with unique, explicitly allowed keys.
func DecodeObject(raw []byte, out any, fields ...string) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return InvalidResponse()
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return err
	}

	keys := make(map[string]bool)

	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}

		name, ok := key.(string)
		if !ok || keys[name] || !slices.Contains(fields, name) {
			return InvalidResponse()
		}

		keys[name] = true

		if err := decoder.Decode(new(json.RawMessage)); err != nil {
			return err
		}
	}

	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(out); err != nil {
		return err
	}

	if decoder.Decode(new(any)) != io.EOF {
		return InvalidResponse()
	}

	return nil
}

func InvalidResponse() error {
	return domain.Failure("MODEL_INVALID_RESPONSE", "O assistente retornou uma resposta inválida. Tente novamente.")
}
