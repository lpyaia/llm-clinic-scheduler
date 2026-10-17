package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/timeouts"
)

func TestRequestDeadlineConfiguration(t *testing.T) {
	for _, test := range []struct {
		name         string
		timeout      time.Duration
		wantDeadline bool
	}{
		{name: "normal", timeout: timeouts.DefaultHTTP, wantDeadline: true},
		{name: "debug", timeout: 0, wantDeadline: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			contexts := make(chan context.Context, 1)
			ping := func(ctx context.Context) error {
				contexts <- ctx

				return nil
			}
			app := New(nil, ping, slog.New(slog.NewTextHandler(io.Discard, nil)), test.timeout)
			req := httptest.NewRequest("GET", "/health/ready", nil)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Error(err)
				}
			}()

			if resp.StatusCode != 200 {
				t.Fatalf("status=%d", resp.StatusCode)
			}

			ctx := <-contexts
			if _, hasDeadline := ctx.Deadline(); hasDeadline != test.wantDeadline {
				t.Fatalf("deadline=%v, want %v", hasDeadline, test.wantDeadline)
			}

			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("request context was not released: %v", ctx.Err())
			}
		})
	}
}

func TestRequestTimeoutReachesDatabaseOperation(t *testing.T) {
	errorsSeen := make(chan error, 1)
	ping := func(ctx context.Context) error {
		<-ctx.Done()

		errorsSeen <- ctx.Err()

		return ctx.Err()
	}
	app := New(nil, ping, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Millisecond)
	req := httptest.NewRequest("GET", "/health/ready", nil)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()

	if resp.StatusCode != 503 {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	if err := <-errorsSeen; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("database operation did not receive timeout: %v", err)
	}
}
