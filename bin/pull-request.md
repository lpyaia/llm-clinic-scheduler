Implementa a primeira entrega do assistente da clínica: um backend determinístico de agendamento em Go com Fiber v3, GORM e PostgreSQL, pronto para ser operado futuramente por um agente.

## Comportamento

- Identificação local por telefone e sessão opaca; catálogo de médicos e especialidades e busca progressiva de disponibilidade.
- Criação, remarcação e cancelamento por ações pendentes com confirmação explícita, expiração e repetição idempotente.
- Transações, bloqueio por paciente, versionamento e índice único parcial para garantir limite semanal, ownership e exclusividade do slot.
- IDs `uuid.UUID` entre domínio, aplicação e persistência, mantendo strings no contrato JSON e campos opcionais nulos/omitidos.

## Desenvolvimento e documentação

- Migrations SQL com goose, seed reproduzível, comando de conclusão de agendamentos e Docker Compose para o PostgreSQL.
- Contrato OpenAPI incorporado ao binário e Swagger UI em `/docs`, com o YAML em `/openapi.yaml`.
- Perfis de debug do VS Code para `serve`, `migrate` e `seed`.
- PRD original, plano de desenvolvimento e README com execução local, regras, exemplos HTTP e testes.

## Validação

- `go test -tags=integration ./... -count=1 -timeout=120s` — aprovado com PostgreSQL real e schemas isolados.
- `golangci-lint run --fix` — sem pendências.
- OpenAPI validado com Redocly.
- Testes cobrem disputa de slot, limite semanal concorrente, ownership, confirmação repetida/expirada, rollback, persistência, remarcação, cancelamento e compatibilidade dos UUIDs no HTTP.

O `.env` local e os binários de desenvolvimento permanecem ignorados pelo Git. O serviço `api` no Compose está comentado por padrão para desenvolvimento e debug com Go local.
