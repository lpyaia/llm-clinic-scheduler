package timeouts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	for _, test := range []struct {
		name, http, command, invalidVariable string
		want                                 Config
	}{
		{name: "defaults", want: Config{HTTPRequest: DefaultHTTP, Command: DefaultCommand}},
		{name: "debug", http: "0s", command: "0s", want: Config{}},
		{name: "custom", http: "30s", command: "5m", want: Config{HTTPRequest: 30 * time.Second, Command: 5 * time.Minute}},
		{name: "invalid HTTP", http: "invalid", invalidVariable: "HTTP_REQUEST_TIMEOUT"},
		{name: "negative HTTP", http: "-1s", invalidVariable: "HTTP_REQUEST_TIMEOUT"},
		{name: "invalid command", command: "invalid", invalidVariable: "COMMAND_TIMEOUT"},
		{name: "negative command", command: "-1s", invalidVariable: "COMMAND_TIMEOUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HTTP_REQUEST_TIMEOUT", test.http)
			t.Setenv("COMMAND_TIMEOUT", test.command)

			got, err := Load()
			if test.invalidVariable != "" {
				if err == nil || !strings.Contains(err.Error(), test.invalidVariable) {
					t.Fatalf("expected configuration error for %s, got %v", test.invalidVariable, err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got != test.want {
				t.Fatalf("want %+v, got %+v", test.want, got)
			}
		})
	}
}

func TestDisabledTimeoutPreservesCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	ctx, cancel := Context(parent, 0)
	defer cancel()

	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		t.Fatal("debug context has an application deadline")
	}

	cancelParent()

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("parent cancellation was lost: %v", ctx.Err())
	}
}

func TestDisabledTimeoutPreservesParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), time.Hour)
	defer cancelParent()

	ctx, cancel := Context(parent, 0)
	defer cancel()

	want, _ := parent.Deadline()
	got, hasDeadline := ctx.Deadline()
	if !hasDeadline || !got.Equal(want) {
		t.Fatalf("parent deadline was lost: want %s, got %s", want, got)
	}
}

func TestPositiveTimeoutStillExpires(t *testing.T) {
	ctx, cancel := Context(context.Background(), 5*time.Millisecond)
	defer cancel()

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("configured timeout was not enforced")
	}
}
