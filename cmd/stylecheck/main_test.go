package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStylecheckCommand(t *testing.T) {
	name := "stylecheck"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	binary := filepath.Join(t.TempDir(), name)
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build checker: %v\n%s", err, output)
	}

	for _, test := range []struct {
		name, file, content, diagnostic string
		fix                             bool
	}{
		{
			name: "unicode and tabs at limit", file: "notes.md",
			content: "\t" + strings.Repeat("é", 116) + "\n",
		},
		{
			name: "long documentation", file: "notes.md", content: strings.Repeat("x", 121),
			diagnostic: "notes.md:1: line has 121 characters",
		},
		{
			name: "long raw string", file: "source.go",
			content:    "package example\nconst value = `" + strings.Repeat("x", 121) + "`\n",
			diagnostic: "source.go:2: line has",
		},
		{
			name: "compact JSON", file: "schema.json", content: `{"type":"object"}`,
			diagnostic: "JSON must use two-space indentation",
		},
		{
			name: "invalid JSON is not rewritten", file: "schema.json", content: `{"type":`, fix: true,
			diagnostic: "invalid JSON",
		},
		{
			name: "invalid UTF-8 JSON", file: "schema.json", content: string([]byte{0xff}),
			diagnostic: "JSON must use UTF-8 encoding",
		},
		{
			name: "NUL in JSON", file: "schema.json", content: "{\x00}",
			diagnostic: "invalid JSON",
		},
		{
			name: "JSON fix preserves long string", file: "schema.json",
			content: `{"description":"` + strings.Repeat("x", 121) + `"}`, fix: true,
			diagnostic: "line has",
		},
		{
			name: "installed skill excluded", file: ".agents/skills/example/SKILL.md",
			content: strings.Repeat("x", 121),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := newRepository(t)
			path := filepath.Join(directory, filepath.FromSlash(test.file))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(path, []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}

			args := []string{}
			if test.fix {
				args = append(args, "-fix")
			}

			command := exec.Command(binary, args...)
			command.Dir = directory

			output, err := command.CombinedOutput()
			if test.diagnostic == "" {
				if err != nil {
					t.Fatalf("valid fixture rejected: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), test.diagnostic) {
				t.Fatalf("expected %q, got error=%v output=%s", test.diagnostic, err, output)
			}

			if test.name == "invalid JSON is not rewritten" {
				content, err := os.ReadFile(path)
				if err != nil || string(content) != test.content {
					t.Fatalf("invalid JSON was modified: content=%q error=%v", content, err)
				}
			}
		})
	}

	t.Run("format and recheck JSON from subdirectory", func(t *testing.T) {
		directory := newRepository(t)
		path := filepath.Join(directory, "schema.json")
		if err := os.WriteFile(path, []byte(`{"nome":"João <teste>","ordem":[1,2]}`), 0o644); err != nil {
			t.Fatal(err)
		}

		subdirectory := filepath.Join(directory, "nested")
		if err := os.Mkdir(subdirectory, 0o755); err != nil {
			t.Fatal(err)
		}

		for _, args := range [][]string{{"-fix"}, {}} {
			command := exec.Command(binary, args...)
			command.Dir = subdirectory
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("format/recheck: %v\n%s", err, output)
			}
		}

		want := "{\n  \"nome\": \"João <teste>\",\n  \"ordem\": [\n    1,\n    2\n  ]\n}\n"
		content, err := os.ReadFile(path)
		if err != nil || string(content) != want {
			t.Fatalf("unexpected formatted JSON: %q error=%v", content, err)
		}
	})
}

func newRepository(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", directory).CombinedOutput(); err != nil {
		t.Fatalf("initialize fixture repository: %v\n%s", err, output)
	}

	return directory
}
