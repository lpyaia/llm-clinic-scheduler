// Package timeouts configures technical deadlines without changing business expiration rules.
package timeouts

import (
	"context"
	"fmt"
	"os"
	"time"
)

const (
	DefaultHTTP    = 10 * time.Second
	DefaultCommand = 2 * time.Minute
)

type Config struct {
	HTTPRequest time.Duration
	Command     time.Duration
}

func Load() (Config, error) {
	httpTimeout, err := duration("HTTP_REQUEST_TIMEOUT", DefaultHTTP)
	if err != nil {
		return Config{}, err
	}

	commandTimeout, err := duration("COMMAND_TIMEOUT", DefaultCommand)
	if err != nil {
		return Config{}, err
	}

	return Config{HTTPRequest: httpTimeout, Command: commandTimeout}, nil
}

func duration(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}

	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s deve ser uma duração não negativa, como 10s, 2m ou 0s", name)
	}

	return value, nil
}

// Context adds no deadline when timeout is zero, but retains parent cancellation,
// inherited deadlines, and the ability to release resources explicitly.
func Context(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout == 0 {
		return context.WithCancel(parent)
	}

	return context.WithTimeout(parent, timeout)
}
