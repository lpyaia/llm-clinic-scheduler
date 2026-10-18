$ErrorActionPreference = 'Stop'

Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    if (-not (Test-Path -LiteralPath '.env' -PathType Leaf)) {
        throw 'Crie a .env na raiz a partir de .env.example antes de executar os testes.'
    }

    & docker compose --profile test up -d --wait test-db
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }

    & go run github.com/joho/godotenv/cmd/godotenv -f .env go test -tags=integration ./... -count=1 -timeout=120s @args
    $testExitCode = $LASTEXITCODE
}
finally {
    Pop-Location
}

exit $testExitCode
