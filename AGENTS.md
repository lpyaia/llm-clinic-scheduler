## Go style

Always run `gofmt` on modified Go files.

Additionally, use blank lines to separate logical sections inside functions.
Prefer visually distinct blocks for:

- input normalization and validation
- object/value construction
- repository/database operations
- error handling
- final result construction

Do not compress an entire function into one continuous block just because gofmt permits it.

After finishing your implementation, run `golangci-lint run --fix` to automatically update formatting.

## Control flow and module interfaces

- Keep use-case coordination readable in execution order. Give one place responsibility for sequencing the steps;
  keep business decisions with the module that owns the relevant state and invariants.
- Prefer direct calls and explicit results. Do not split a straight-line operation across callbacks merely to keep
  methods on different types or to satisfy a responsibility statement literally.
- Avoid orchestration that alternates between the same modules through callbacks. For example,
  `Agent -> flow.reply -> callback -> Agent.reply -> flow.request` obscures who controls the turn.
  Reorganize the coordination or the module interface so that the caller can follow the operation in one direction.
- A callback must represent a concrete variation or a well-defined scoped operation, such as a transaction.
  Its contract must make supplied state, allowed operations, lifetime and failure behavior explicit.
  Do not require callbacks to capture and re-enter the object invoking them to complete that object's own operation.
  For scoped access, prefer passing the capability needed by the callback, such as a transaction handle.
- Moving coordination must preserve lock duration, cancellation, intermediate saves and rollback behavior.
  Document required ordering at the interface; do not replace hidden callback coupling with undocumented
  `begin`/`finish` obligations spread across callers.
- Introduce an interface, callback or forwarding layer only when it hides meaningful complexity or supports
  a concrete variation required by the current work. State that purpose when proposing the abstraction.
- During design and review, trace one complete successful path and one failure path across the changed modules.
  Identify who sequences the work, owns each state transition, holds and releases access, and commits changes.
  Flag unnecessary round trips even when individual methods have narrow responsibilities and all tests pass.

## Text and schema formatting

- Limit maintained text files to 120 characters per line, including tests, comments and string literals.
  Count each tab as four characters. Do not suppress the limit for individual lines or files.
- Break long calls, composite literals and conditions into readable logical sections.
- Store production JSON schemas in `internal/agent/schemas/*.json` and embed them through `schemas.go`.
  Use two-space JSON indentation, one property or array item per line and a final newline.
  Small JSON test fixtures may stay inline; format longer fixtures across multiple lines.
- Run `golangci-lint fmt` to wrap Go code, then `gofmt` on modified Go files.
  Formatting does not replace validation: run `golangci-lint run --fix` and `go run ./cmd/stylecheck`.
- Run `go run ./cmd/stylecheck -fix` to format maintained JSON files. It does not wrap long strings.
  Split those strings at safe boundaries or restructure the surrounding code without changing their value.
- The general check covers tracked and unignored new text files, including Markdown, SQL, YAML and scripts.
  It excludes installed skills (`.agents/`), generated publication artifacts (`.scratch/`), build output (`bin/`),
  vendored dependencies (`vendor/`), dependency locks (`go.sum`, `skills-lock.json`) and binary files.
  Keep these exclusions synchronized with `cmd/stylecheck`; do not add exclusions to hide authored code violations.
- The formatting workflow must pass before merging.

## Version control

When you finish an implementation, or if you get asked, you should commit all the changes you've made. If there's no
branch created, create one.
For conventions purpose, the branch name should follow this pattern:
<feat|fix|perf|refactor>/<brief-and-meaningful-description-of-the-change>.
If the current branch is already merged to main, or the current change does not fit to the current branch's intention,
suggest the creation of a new one before proceeding.

## Agent skills

### Issue tracker

GitHub Issues in `lpyaia/llm-clinic-scheduler`, tracked in [Project 1](https://github.com/users/lpyaia/projects/1).
See [docs/agents/issue-tracker.md](docs/agents/issue-tracker.md).

### Triage labels

Use the default triage labels, including `ready-for-agent` for approved implementation tickets. See
[docs/agents/triage-labels.md](docs/agents/triage-labels.md).

### Domain docs

Single-context layout: root `GLOSSARY.md` and `docs/adr/`, created only when needed. See
[docs/agents/domain.md](docs/agents/domain.md).
