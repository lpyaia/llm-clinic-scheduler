// Command stylecheck checks line length and JSON formatting in maintained text files.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxLineLength = 120
	tabWidth      = 4
)

func main() {
	fix := flag.Bool("fix", false, "format JSON files before checking (does not wrap long lines)")

	flag.Parse()

	if err := run(*fix, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(fix bool, diagnostics io.Writer) error {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("locate repository: %w", err)
	}

	directory := strings.TrimSpace(string(root))
	command := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	command.Dir = directory

	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("list repository files: %w", err)
	}

	issues := 0

	seen := make(map[string]bool)
	for _, name := range strings.Split(string(output), "\x00") {
		if name == "" || seen[name] || excluded(name) {
			continue
		}

		seen[name] = true

		count, err := checkFile(filepath.Join(directory, filepath.FromSlash(name)), name, fix, diagnostics)
		if err != nil {
			return err
		}

		issues += count
	}

	if issues > 0 {
		return fmt.Errorf("stylecheck: %d issue(s); limit is %d characters (tabs count as %d)",
			issues, maxLineLength, tabWidth)
	}

	_, _ = fmt.Fprintln(diagnostics, "stylecheck: line length and JSON formatting passed")

	return nil
}

func excluded(name string) bool {
	// Installed skills, generated publication/build artifacts, vendored code and dependency locks.
	for _, prefix := range []string{".agents/", ".scratch/", "bin/", "vendor/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return name == "go.sum" || name == "skills-lock.json"
}

func checkFile(path, name string, fix bool, diagnostics io.Writer) (int, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, nil // A tracked deletion still appears in git ls-files before staging.
	}

	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", name, err)
	}

	if !info.Mode().IsRegular() {
		return 0, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", name, err)
	}

	if strings.HasSuffix(name, ".json") && !utf8.Valid(data) {
		_, _ = fmt.Fprintf(diagnostics, "%s: JSON must use UTF-8 encoding\n", name)
		return 1, nil
	}

	if !strings.HasSuffix(name, ".json") && (bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)) {
		return 0, nil // Binary files are outside the text formatting policy.
	}

	// Accept either checkout line ending; canonical JSON output uses LF.
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	issues := 0

	if strings.HasSuffix(name, ".json") {
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, bytes.TrimSpace(data), "", "  "); err != nil {
			_, _ = fmt.Fprintf(diagnostics, "%s: invalid JSON: %v\n", name, err)

			issues++
		} else {
			formatted.WriteByte('\n')

			if !bytes.Equal(data, formatted.Bytes()) {
				if fix {
					data = formatted.Bytes()
					if err := os.WriteFile(path, data, info.Mode().Perm()); err != nil {
						return 0, fmt.Errorf("format %s: %w", name, err)
					}
				} else {
					_, _ = fmt.Fprintf(
						diagnostics,
						"%s: JSON must use two-space indentation; run go run ./cmd/stylecheck -fix\n",
						name,
					)

					issues++
				}
			}
		}
	}

	for index, line := range strings.Split(string(data), "\n") {
		length := utf8.RuneCountInString(line) + strings.Count(line, "\t")*(tabWidth-1)
		if length > maxLineLength {
			_, _ = fmt.Fprintf(diagnostics, "%s:%d: line has %d characters (maximum %d)\n",
				name, index+1, length, maxLineLength)

			issues++
		}
	}

	return issues, nil
}
