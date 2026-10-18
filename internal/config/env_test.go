package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEnvFromModuleRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "example")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(root, "go.mod"), "module example\n")
	writeFile(t, filepath.Join(root, ".env"), "CLINIC_CONFIG_TEST='root value'\nHTTP_REQUEST_TIMEOUT=10s\n")
	writeFile(t, filepath.Join(nested, ".env"), "CLINIC_CONFIG_TEST=nested\n")
	t.Chdir(nested)
	t.Setenv("HTTP_REQUEST_TIMEOUT", "0s")

	// Restore the initial state after testing a key absent from the process.
	t.Setenv("CLINIC_CONFIG_TEST", "")

	if err := os.Unsetenv("CLINIC_CONFIG_TEST"); err != nil {
		t.Fatal(err)
	}

	if err := LoadEnv(); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("CLINIC_CONFIG_TEST"); got != "root value" {
		t.Fatalf("expected module root value, got %q", got)
	}

	if got := os.Getenv("HTTP_REQUEST_TIMEOUT"); got != "0s" {
		t.Fatalf("debug override was lost: %q", got)
	}
}

func TestLoadEnvWithoutFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example\n")
	t.Chdir(root)
	t.Setenv("HTTP_REQUEST_TIMEOUT", "30s")

	if err := LoadEnv(); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("HTTP_REQUEST_TIMEOUT"); got != "30s" {
		t.Fatalf("process configuration changed: %q", got)
	}
}

func TestLoadEnvRejectsMalformedFileWithoutExposingValues(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example\n")
	writeFile(t, filepath.Join(root, ".env"), "SECRET='private-value\n")
	t.Chdir(root)

	err := LoadEnv()
	if err == nil {
		t.Fatal("expected an invalid dotenv error")
	}

	if strings.Contains(err.Error(), "private-value") {
		t.Fatalf("configuration error exposed a value: %v", err)
	}
}
