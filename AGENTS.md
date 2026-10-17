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
