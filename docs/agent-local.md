# Consulta conversacional de agendamentos — ticket #7

O paciente autenticado consulta seus agendamentos em português por
`POST /v1/agent/messages`. A ferramenta `list_appointments` reutiliza
`application.Service.ListAppointments`; somente o middleware de sessão fornece
a identidade. Nem o corpo HTTP nem os argumentos da ferramenta aceitam
`patient_id`. Não há ferramentas de mutação nesta entrega.

## Execução local com OpenAI

1. Crie `.env` a partir de `.env.example` se necessário. Defina `OPENAI_API_KEY`
   com uma chave da sua conta de API e mantenha o arquivo fora do Git. O padrão
   de `OPENAI_MODEL` é `gpt-4.1-mini`; alterações exigem um modelo compatível com
   Chat Completions, function calling estrito e Structured Outputs. O adapter
   usa `https://api.openai.com/v1/chat/completions`, sem streaming.
   Para `OPENAI_MODEL=gpt-6-luna`, o adapter envia `reasoning_effort: "none"`,
   exigido pelo modelo para usar function calling nesse endpoint.
2. Inicie o banco e aplique os dados iniciais:

   ```powershell
   docker compose up -d --wait db
   go run ./cmd/clinic migrate
   go run ./cmd/clinic seed
   go run ./cmd/clinic serve
   ```

3. Em outro terminal, emita uma sessão e envie uma pergunta:

   ```powershell
   $base = 'http://127.0.0.1:3000'
   $body = @{ full_name = 'Maria Souza'; phone = '+5511999999999' } | ConvertTo-Json
   $session = Invoke-RestMethod "$base/v1/dev/sessions" -Method Post -ContentType 'application/json' -Body $body
   $headers = @{ Authorization = "Bearer $($session.token)" }
   $question = @{ message = 'Quais atendimentos tenho marcados?' } | ConvertTo-Json
   Invoke-RestMethod "$base/v1/agent/messages" -Method Post -Headers $headers -ContentType 'application/json; charset=utf-8' -Body ([System.Text.Encoding]::UTF8.GetBytes($question))
   ```

   Uma conta nova recebe `appointments: []` e uma mensagem informando que não há
   atendimentos para os filtros consultados. Para verificar uma lista preenchida,
   agende um atendimento com essa sessão pelo fluxo **Fluxo completo via HTTP**
   do README e repita a pergunta. Use outra sessão para verificar o isolamento.
   Também é possível testar pela Swagger UI em `/docs`.

4. Perguntas como “Liste meus agendamentos cancelados” usam o filtro de status.
   A ferramenta aceita `status` (`scheduled`, `cancelled`, `completed` ou vazio
   para todos), `from` e `to` (RFC 3339, intervalo `[from,to)`). Valores nulos ou
   ausentes usam `scheduled` e não restringem datas. Datas relativas e memória
   entre mensagens fazem parte de tickets seguintes; use datas explícitas aqui.

## Execução e proteção dos resultados

`agent.Model` recebe mensagens, definições de ferramentas e o schema da resposta;
devolve chamadas estruturadas ou um objeto JSON final. Tipos OpenAI ficam no
adapter. Trocar esse adapter por um modelo simulado não altera o Scheduling Core.

O loop aceita uma chamada de ferramenta por etapa e no máximo quatro chamadas ao
modelo. Valida nome, ID, argumentos, campos desconhecidos, chaves duplicadas,
status e intervalo antes de acessar o núcleo. Rejeita resposta final sem consulta
prévia, chamadas repetidas com o mesmo ID, respostas incompletas, recusas e
respostas misturando conteúdo final e ferramentas.

O modelo devolve `appointment_ids` com todos os IDs da última consulta, na mesma
ordem. O servidor verifica essa lista e monta a mensagem em português com os
dados reais, inclusive a ausência de resultados. O resultado da ferramenta e a
resposta HTTP omitem identidade, telefone e outros campos desnecessários.

O histórico existe somente durante a requisição. O timeout HTTP configurado se
propaga ao modelo e ao banco; o cliente do fornecedor também limita cada chamada
a 30 segundos, inclusive quando o timeout HTTP está desabilitado para debug.
Respostas do fornecedor têm limite de 1 MiB e 2048 tokens de saída. O adapter
envia `store: false`. A mensagem do paciente e os dados da consulta são enviados
ao fornecedor configurado; use dados fictícios no roteiro local. Logs HTTP não
registram esse conteúdo nem a chave. Uma interação bem-sucedida normalmente usa
duas chamadas de API e está sujeita à cobrança da conta do fornecedor.

Erros mantêm o envelope HTTP existente: `INVALID_INPUT` (400), `UNAUTHORIZED`
(401), `MODEL_INVALID_RESPONSE` ou `AGENT_STEP_LIMIT` (502), `AGENT_UNAVAILABLE`
(503). Falhas de transporte/fornecedor e cancelamentos retornam `INTERNAL_ERROR`
(500), sem detalhes internos. Sem chave, os outros endpoints continuam disponíveis.

## Verificação

```powershell
go test ./internal/agent ./internal/adapters/httpapi ./internal/adapters/openai -count=1
go test ./...
go vet ./...
golangci-lint run --fix
.\scripts\test-integration.ps1
```

Os testes usam modelos simulados, o núcleo real e repositórios em memória na
fronteira de banco. O adapter é verificado contra um servidor HTTP local, incluindo
tool calls, resultados correlacionados, schema final, falhas e cancelamento. Não
consomem créditos nem exigem chave OpenAI. A suíte PostgreSQL usa o banco exclusivo
de testes e exige Docker ativo. A execução real do roteiro é uma verificação
manual adicional que exige chave e acesso à API.

Referências de protocolo: [function calling](https://developers.openai.com/api/docs/guides/function-calling),
[Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs?api-mode=chat)
e [GPT-4.1 mini](https://developers.openai.com/api/docs/models/gpt-4.1-mini).
