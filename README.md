# LLM Clinic Scheduler

Backend de aprendizado para um assistente de pacientes. A fase 1 funciona integralmente sem IA: Go, Fiber v3, GORM, PostgreSQL 17 e migrations SQL com goose.

O backend protege disponibilidade, autorização, limite semanal e concorrência. Futuramente, o agente interpretará linguagem e chamará os mesmos casos de uso.

## Documentação

- [PRD original](docs/prd.md): requisitos de produto fornecidos para o projeto.
- [Plano de desenvolvimento aprovado](docs/development-plan.md): roadmap, arquitetura com Fiber e GORM, regras, contratos e critérios de aceite.
- [Contrato OpenAPI](api/openapi.yaml): especificação da API REST da fase 1.
- [Swagger UI local](http://127.0.0.1:3000/docs): documentação interativa com execução de requisições.

## Executar localmente

Pré-requisitos: Go 1.27+, Docker Desktop com engine Linux ativo e Docker Compose.

```powershell
docker compose up -d --wait db
go run ./cmd/clinic migrate
go run ./cmd/clinic seed
go run ./cmd/clinic serve
```

A API escuta em `http://127.0.0.1:3000`. `migrate` aplica migrations pendentes; `seed` adiciona dois médicos e slots de dias úteis nas próximas oito semanas, sem duplicar dados em execuções repetidas.

Configuração por variáveis de ambiente; [.env.example](.env.example) documenta os valores padrão. O executável Go não carrega `.env` automaticamente. Exemplo:

```powershell
$env:DATABASE_URL = 'postgres://clinic:clinic@localhost:5432/clinic?sslmode=disable'
$env:HTTP_ADDR = '127.0.0.1:3000'
$env:CLINIC_TIMEZONE = 'America/Sao_Paulo'
$env:HTTP_REQUEST_TIMEOUT = '10s'
$env:COMMAND_TIMEOUT = '2m'
```

Para executar também a API em container:

O serviço `api` está comentado no [compose.yaml](compose.yaml), mantendo apenas o PostgreSQL ativo por padrão para desenvolvimento e debug locais. Descomente esse serviço antes de executar os comandos abaixo.

```powershell
docker compose build api
docker compose up -d --wait db
docker compose run --rm api migrate
docker compose run --rm api seed
docker compose up -d api
```

O comando de manutenção conclui agendamentos cujo término já passou:

```powershell
go run ./cmd/clinic complete-due
```

As migrations são executadas explicitamente. Não há `AutoMigrate` na inicialização. O banco persiste no volume `postgres_data`; `docker compose stop` interrompe os serviços preservando os dados.

## Debugger no VS Code

As configurações de [.vscode/launch.json](.vscode/launch.json) executam o pacote `cmd/clinic` com Delve e carregam as variáveis do arquivo `.env` local, ignorado pelo Git. Crie-o a partir do exemplo caso ainda não exista:

```powershell
if (-not (Test-Path .env)) { Copy-Item .env.example .env }
docker compose up -d --wait db
```

Instale a extensão **Go** (`golang.go`), recomendada pelo workspace. Se o VS Code solicitar o debugger, instale `dlv` pela opção **Go: Install/Update Tools**.

Abra **Run and Debug** (`Ctrl+Shift+D`), selecione **Go: serve**, **Go: migrate** ou **Go: seed**, adicione seus breakpoints e pressione **F5**. Os três perfis executam Go localmente e usam o PostgreSQL exposto na porta 5432.

No primeiro uso, execute **Go: migrate**, depois **Go: seed** e finalmente **Go: serve**. Os comandos `migrate` e `seed` encerram ao concluir. Para investigar esses comandos desde o início, coloque um breakpoint em `run`, no arquivo `cmd/clinic/main.go`; as migrations SQL são executadas pelo goose, sem breakpoints dentro do SQL.

Se a API já estiver executando no Docker, use `docker compose stop api` antes de depurar `serve`, para liberar a porta 3000. O banco pode continuar em execução. Para mudar conexão, endereço ou fuso durante o debug, edite `.env`.

Os três perfis definem `HTTP_REQUEST_TIMEOUT=0s` e `COMMAND_TIMEOUT=0s`, sobrescrevendo o `.env` apenas na sessão de debug. Assim, breakpoints longos não esgotam os deadlines impostos pela aplicação. Reinicie a sessão de debug após alterar essas configurações; um contexto já expirado não é recuperado.

Fora desses perfis, os padrões continuam sendo dez segundos por requisição HTTP e dois minutos para `migrate`, `seed` e `complete-due`. As variáveis aceitam durações como `30s` e `5m`; `0s` remove o deadline próprio, preservando cancelamento e eventuais deadlines do contexto pai. Valores negativos ou inválidos são rejeitados na inicialização. O timeout de comandos só é criado para os comandos de manutenção, sem um timer adicional durante `serve`.

O debugger não pausa o relógio do sistema: ações pendentes ainda expiram em dez minutos e sessões em 24 horas. Timeouts do cliente HTTP ou do PostgreSQL também são independentes dessas configurações. O limite de dez segundos para encerramento gracioso do servidor continua ativo.

## Swagger UI

Com a API em execução, acesse [http://127.0.0.1:3000/docs](http://127.0.0.1:3000/docs). O contrato também está disponível em [http://127.0.0.1:3000/openapi.yaml](http://127.0.0.1:3000/openapi.yaml). Essas duas rotas são públicas; as rotas de negócio continuam exigindo sessão.

1. Expanda `POST /v1/dev/sessions`, clique em **Try it out** e envie nome e telefone para emitir uma sessão.
2. Copie o campo `token` da resposta.
3. Clique em **Authorize**, informe apenas o token, sem o prefixo `Bearer`, e confirme.
4. Use **Try it out** nos demais endpoints. Nas mutações, prepare a ação, confira seu resumo e confirme pelo ID retornado.

As requisições usam a mesma origem em que a documentação foi aberta, inclusive ao alterar a porta da API. A autorização não é persistida após recarregar a página.

O HTML e o contrato são incorporados ao binário, funcionando também no container sem arquivos externos. Os assets do Swagger UI usam a versão fixada `5.33.3` via jsDelivr; o navegador precisa de acesso à internet para carregá-los. Ao editar o contrato, recompile/reinicie a aplicação ou reconstrua a imagem Docker.

## Fluxo completo via HTTP

Execute em outro terminal PowerShell após iniciar a API:

```powershell
$base = 'http://127.0.0.1:3000'
$sessionBody = @{ full_name = 'Maria Souza'; phone = '+5511999999999' } | ConvertTo-Json
$session = Invoke-RestMethod "$base/v1/dev/sessions" -Method Post -ContentType 'application/json' -Body $sessionBody
$headers = @{ Authorization = "Bearer $($session.token)" }

$specialties = Invoke-RestMethod "$base/v1/specialties" -Headers $headers
$doctors = Invoke-RestMethod "$base/v1/doctors" -Headers $headers
$doctor = $doctors[0]
$availability = Invoke-RestMethod "$base/v1/slots?doctor_id=$($doctor.id)&period=afternoon" -Headers $headers
$slot = $availability.slots[0]

# Preparar não reserva o horário. Exiba o resumo e confirme explicitamente.
$body = @{ operation = 'create_appointment'; slot_id = $slot.id; category = 'consultation' } | ConvertTo-Json
$action = Invoke-RestMethod "$base/v1/pending-actions" -Headers $headers -Method Post -ContentType 'application/json' -Body $body
$action.summary
$confirmation = Read-Host 'Digite SIM para confirmar'
if ($confirmation -ceq 'SIM') {
    $appointment = Invoke-RestMethod "$base/v1/pending-actions/$($action.id)/confirm" -Headers $headers -Method Post
    $appointment
}

Invoke-RestMethod "$base/v1/appointments?status=scheduled" -Headers $headers
```

Para remarcar, prepare uma ação com `operation=reschedule_appointment`, `appointment_id` e outro `slot_id` do mesmo médico. Para cancelar, use `operation=cancel_appointment` e `appointment_id`. Nos dois casos, apresente o resumo ao paciente antes de chamar a mesma rota de confirmação.

`GET /v1/pending-actions/{id}` consulta a operação e seu resultado. Repetir a confirmação de uma ação concluída retorna a resposta original, inclusive depois de reiniciar o processo. A rota de confirmação aceita corpo vazio ou `{}`; não aceita novos parâmetros.

Contrato completo: [api/openapi.yaml](api/openapi.yaml). Todas as rotas de negócio usam Bearer token; health checks e criação de sessão são públicos.

## Regras

- Identificação local por telefone E.164, sem senha ou OTP; nome obrigatório no cadastro. A sessão dura 24 horas e só seu hash é armazenado. `identity_verified` permanece `false`. A rota `/dev/sessions` serve ao ambiente de desenvolvimento e não comprova posse do telefone.
- Categorias: `consultation`, `exam`, `procedure`. Um médico possui uma especialidade e atende qualquer categoria nesta fase.
- Slots de 30 minutos, em hora cheia ou meia hora. O banco rejeita slots duplicados e fora da grade.
- Até três agendamentos por semana civil, segunda a domingo, no fuso da clínica. `scheduled` e `completed` contam; `cancelled` não.
- Datas de entrada em RFC 3339. Intervalos são `[from,to)`. A disponibilidade sempre exclui horários já iniciados; início passado é ajustado ao instante atual.
- Busca exige médico ou especialidade; ambos juntos aplicam interseção. Sem `to`, começa com duas semanas e amplia uma semana por tentativa até oito. Com `to`, respeita o período explícito. Retorna `searched_from`, `searched_until` e `expanded`.
- Períodos: `morning` das 06h às 12h, `afternoon` das 12h às 18h e `evening` das 18h às 24h. Respostas usam o fuso configurado.
- Criar, remarcar e cancelar exigem ação pendente, válida por dez minutos. O servidor revalida as regras na confirmação; preparar não bloqueia disponibilidade.
- Somente agendamentos `scheduled` ainda não iniciados podem ser alterados. Remarcar preserva médico, categoria e ID do agendamento. Falhas preservam o slot original.
- Ações com conflito de negócio tornam-se terminais e exigem nova preparação. Falhas transitórias fazem rollback e permitem repetir a confirmação.
- Recursos alheios retornam `404`. Não é possível escolher o paciente por campos enviados na operação.

Erros usam `{ "error": { "code": "SLOT_UNAVAILABLE", "message": "...", "request_id": "..." } }`. Códigos principais: `INVALID_INPUT` (400), `UNAUTHORIZED` (401), `NOT_FOUND` (404), `SLOT_UNAVAILABLE`, `WEEKLY_LIMIT_REACHED`, `APPOINTMENT_CHANGED`, `APPOINTMENT_NOT_MUTABLE` (409), `ACTION_EXPIRED` (410) e `INTERNAL_ERROR` (500). Readiness sem banco retorna `NOT_READY` (503).

## Arquitetura e concorrência

```text
cmd/clinic                         composição, configuração e comandos
internal/adapters/httpapi           Fiber, DTOs, sessão e tradução de erros
internal/application                casos de uso e interfaces transacionais
internal/domain                     entidades, estados e regras puras
internal/infrastructure/postgres     GORM, modelos, migrations e manutenção
```

`fiber.Ctx`, modelos GORM e `*gorm.DB` não atravessam as fronteiras do domínio e da aplicação. Os handlers recebem um contexto com timeout configurável, de dez segundos por padrão e desabilitado nos perfis de debug. O relógio é injetado para testar datas e expiração.

Os IDs de entidades e os parâmetros dos casos de uso e repositórios utilizam `uuid.UUID`. Strings recebidas por HTTP são convertidas e validadas no adapter antes de chegar à aplicação; UUIDs obrigatórios não podem ser `uuid.Nil`. No JSON, os IDs continuam sendo strings. Na persistência, IDs opcionais de ações usam `*uuid.UUID` para representar `NULL`; na resposta, campos ausentes continuam omitidos. As colunas já eram `uuid`, portanto essa mudança de tipos Go não exige uma migration de banco.

Confirmações adquirem locks na ordem paciente → ação → agendamento existente → slot de destino. O lock do paciente serializa mutações antes da contagem semanal. Um índice único parcial sobre o slot protege reservas `scheduled` e `completed`, inclusive contra gravações que contornem o serviço.

A alteração e o resultado da ação são gravados na mesma transação. Um savepoint permite desfazer uma falha de negócio na gravação e ainda registrar a ação como falha. Erros de infraestrutura desfazem toda a confirmação. Versões impedem executar ações preparadas antes de outra alteração do agendamento.

Logs estruturados incluem ID da requisição, método, padrão da rota, duração, status e código do erro. Não registram corpos, tokens, telefones ou nomes.

## Testes

```powershell
go test ./...
go vet ./...
docker compose up -d --wait db
$env:TEST_DATABASE_URL = 'postgres://clinic:clinic@localhost:5432/clinic?sslmode=disable'
go test -tags=integration ./... -count=1 -timeout=120s
```

Os testes de integração criam um schema exclusivo por teste e removem apenas esse schema. Exigem uma URL PostgreSQL e permissão para criar schemas. A suíte sem a tag não depende de banco. Concorrência e restrições são verificadas no PostgreSQL real, sem substituir por SQLite.

Cobertura funcional: disputa por slot, limite semanal concorrente, confirmação repetida e expirada, ownership, reinício, remarcação em semana cheia, rollback com falha injetada, filtros, expansão, manutenção e ciclo HTTP completo. Testes de domínio verificam também a passagem entre domingo e segunda-feira no fuso da clínica.

## Próximas fases

1. Agente em português, abstração de LLM, tool calling, loop limitado e estado em memória. Tools reutilizam os casos de uso; confirmações ficam ligadas ao ID da ação pendente.
2. Traces, métricas de tokens/latência/custo e avaliações com dataset manual e relógio controlado.
3. RAG com documentos da clínica, ingestão, embeddings, pgvector, citações e abstention.
4. Avaliações de recall@k, groundedness, citações, chunking e top-k.
5. Conversas e mensagens persistidas, com serialização por conversa.
6. Adapter WhatsApp com deduplicação de mensagens e domínio independente do canal.

Frontend, IA, RAG, WhatsApp e deployment em produção não fazem parte desta entrega.
