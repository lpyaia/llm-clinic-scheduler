$ErrorActionPreference = 'Stop'

Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    if (-not (Test-Path -LiteralPath '.env' -PathType Leaf)) {
        throw 'Crie a .env na raiz a partir de .env.example antes de executar os testes.'
    }

    & go run github.com/joho/godotenv/cmd/godotenv -f .env go test ./... -count=1 @args
    $testExitCode = $LASTEXITCODE
}
finally {
    Pop-Location
}

exit $testExitCode
