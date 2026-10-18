package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/httpapi"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/config"
	"github.com/lpyaia/llm-clinic-scheduler/internal/infrastructure/postgres"
	"github.com/lpyaia/llm-clinic-scheduler/internal/timeouts"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("command_failed", "error", err.Error())
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	if command != "serve" && command != "migrate" && command != "seed" && command != "complete-due" {
		return fmt.Errorf("use: clinic [serve|migrate|seed|complete-due]")
	}

	if err := config.LoadEnv(); err != nil {
		return err
	}

	timeoutConfig, err := timeouts.Load()
	if err != nil {
		return err
	}

	loc, err := time.LoadLocation(env("CLINIC_TIMEZONE", "America/Sao_Paulo"))
	if err != nil {
		return err
	}

	store, err := postgres.Open(env("DATABASE_URL", "postgres://clinic:clinic@localhost:5432/clinic?sslmode=disable"))
	if err != nil {
		return errors.New("não foi possível conectar ao PostgreSQL; verifique DATABASE_URL e o serviço do banco")
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Error("database_close_failed", "error", err.Error())
		}
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if command != "serve" {
		ctx, cancel := timeouts.Context(signalCtx, timeoutConfig.Command)
		defer cancel()

		switch command {
		case "migrate":
			return store.Migrate(ctx)
		case "seed":
			return store.Seed(ctx, time.Now(), loc)
		case "complete-due":
			count, err := store.CompleteDue(ctx, time.Now())
			if err == nil {
				logger.Info("appointments_completed", "count", count)
			}

			return err
		}
	}

	service := &application.Service{Repo: store, Clock: application.SystemClock{}, Location: loc}
	app := httpapi.New(service, store.Ping, logger, timeoutConfig.HTTPRequest)

	stopped := make(chan struct{})

	go func() {
		select {
		case <-signalCtx.Done():
			_ = app.ShutdownWithTimeout(10 * time.Second)
		case <-stopped:
		}
	}()

	err = app.Listen(env("HTTP_ADDR", "127.0.0.1:3000"))

	close(stopped)

	return err
}
