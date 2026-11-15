package trace

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
)

type modelKey struct{ provider, model string }

type rate struct {
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Input    *float64 `json:"input_per_million"`
	Output   *float64 `json:"output_per_million"`
}

// Pricing is an immutable, operator-supplied table; no provider prices are built in.
type Pricing struct {
	currency string
	rates    map[modelKey]rate
}

// LoadPricing requires explicit nonnegative rates, unique provider/model pairs and
// one currency. Errors deliberately omit file contents, which may contain secrets.
func LoadPricing(reader io.Reader) (*Pricing, error) {
	invalid := errors.New("invalid model pricing table")

	var input struct {
		Currency string `json:"currency"`
		Rates    []rate `json:"rates"`
	}

	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF ||
		!regexp.MustCompile(`^[A-Z]{3}$`).MatchString(input.Currency) || len(input.Rates) == 0 {
		return nil, invalid
	}

	pricing := &Pricing{currency: input.Currency, rates: make(map[modelKey]rate)}
	for _, item := range input.Rates {
		key := modelKey{item.Provider, item.Model}
		_, duplicate := pricing.rates[key]
		if item.Provider == "" || item.Model == "" || duplicate || !validRate(item.Input) || !validRate(item.Output) {
			return nil, invalid
		}

		pricing.rates[key] = item
	}

	return pricing, nil
}

func validRate(value *float64) bool {
	return value != nil && *value >= 0 && !math.IsNaN(*value) && !math.IsInf(*value, 0)
}
