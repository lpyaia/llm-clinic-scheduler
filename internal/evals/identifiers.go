package evals

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// identifiers preserves fixture IDs and assigns stable UUIDs to IDs created during a run.
// Only reports and replay inputs are translated; application randomness is never changed globally.
type identifiers struct {
	scenario string
	forward  map[string]string
	reverse  map[string]string
	count    int
}

func newIdentifiers(scenario Scenario) *identifiers {
	ids := &identifiers{scenario: scenario.ID, forward: map[string]string{}, reverse: map[string]string{}}
	encoded, _ := json.Marshal(scenario)
	for _, id := range uuidPattern.FindAllString(string(encoded), -1) {
		ids.forward[strings.ToLower(id)] = id
		ids.reverse[id] = id
	}

	return ids
}

func (ids *identifiers) normalize(encoded []byte) []byte {
	return uuidPattern.ReplaceAllFunc(encoded, func(raw []byte) []byte {
		id := strings.ToLower(string(raw))
		if stable, ok := ids.forward[id]; ok {
			return []byte(stable)
		}

		ids.count++
		stable := uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "%s/%d", ids.scenario, ids.count)).String()
		ids.forward[id], ids.reverse[stable] = stable, id
		ids.forward[stable] = stable

		return []byte(stable)
	})
}

func (ids *identifiers) restore(encoded []byte) []byte {
	return uuidPattern.ReplaceAllFunc(encoded, func(raw []byte) []byte {
		if actual, ok := ids.reverse[string(raw)]; ok {
			return []byte(actual)
		}

		return raw
	})
}

func normalized[T any](ids *identifiers, value T) (T, error) {
	var result T

	encoded, err := json.Marshal(value)
	if err != nil {
		return result, err
	}

	err = json.Unmarshal(ids.normalize(encoded), &result)

	return result, err
}
