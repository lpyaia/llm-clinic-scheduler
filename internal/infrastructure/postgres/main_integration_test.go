//go:build integration

package postgres

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lpyaia/llm-clinic-scheduler/internal/config"
)

func TestMain(m *testing.M) {
	if err := config.LoadEnv(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := validateTestDatabase(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func validateTestDatabase() error {
	testURL := os.Getenv("TEST_DATABASE_URL")
	if testURL == "" {
		return errors.New("defina TEST_DATABASE_URL na .env da raiz para um banco exclusivo de testes")
	}

	testConfig, err := pgx.ParseConfig(testURL)
	if err != nil || testConfig.Database == "" {
		return errors.New("TEST_DATABASE_URL inválida; informe um banco exclusivo de testes")
	}

	appURL := os.Getenv("DATABASE_URL")
	if appURL == "" {
		appURL = "postgres://clinic:clinic@localhost:5432/clinic?sslmode=disable"
	}

	appConfig, err := pgx.ParseConfig(appURL)
	if err != nil {
		return errors.New("DATABASE_URL inválida; não foi possível verificar o isolamento do banco de testes")
	}

	if testConfig.Database == appConfig.Database {
		return errors.New("TEST_DATABASE_URL deve usar um nome de banco diferente de DATABASE_URL")
	}

	return nil
}

func TestValidateTestDatabase(t *testing.T) {
	for _, test := range []struct {
		name, appURL, testURL string
		valid                 bool
	}{
		{name: "dedicated", appURL: "postgres://localhost/clinic", testURL: "postgres://localhost/clinic_test", valid: true},
		{name: "missing", appURL: "postgres://localhost/clinic"},
		{name: "invalid test URL", testURL: "postgres://localhost:invalid/clinic_test"},
		{name: "same database", appURL: "postgres://localhost/clinic", testURL: "postgres://127.0.0.1/clinic"},
		{name: "same default database", testURL: "postgres://localhost/clinic"},
		{name: "invalid app URL", appURL: "postgres://localhost:invalid/clinic", testURL: "postgres://localhost/clinic_test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", test.appURL)
			t.Setenv("TEST_DATABASE_URL", test.testURL)

			err := validateTestDatabase()
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, got error %v", test.valid, err)
			}
		})
	}
}
