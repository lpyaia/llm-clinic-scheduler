# Investigar uma execução por trace — #16

Cada turno de `Agent.Reply` ou `Agent.ReplyConversation` produz uma execução independente, com `trace_id`,
`request_id` e `conversation_id`. O `request_id` corresponde ao header HTTP `X-Request-ID` e ao log `http_request`.
Mensagens posteriores mantêm a conversa e recebem novos IDs de execução e requisição.
Chamadas diretas ao agente recebem um `request_id` gerado quando o contexto não fornece um UUID válido.

## Captura local

1. Configure o banco, o modelo e uma sessão conforme [agent-local.md](agent-local.md).
   Use exclusivamente pacientes, mensagens e agendamentos simulados neste roteiro.
2. Antes de iniciar o servidor, prepare o arquivo de saída:

   ```powershell
   New-Item -ItemType Directory -Force traces | Out-Null
   $env:CLINIC_TRACE_FILE = 'traces/agent.jsonl'
   $env:CLINIC_TRACE_SYNTHETIC = 'true'
   go run ./cmd/clinic serve
   ```

   Também é possível configurar essas variáveis na `.env`.
   `CLINIC_TRACE_FILE` vazio desativa a instrumentação. Sem `CLINIC_TRACE_SYNTHETIC=true`,
   os traces contêm somente metadados, sem mensagens, argumentos, resultados ou respostas.
   O diretório `traces/` está fora do Git. O arquivo recebe uma linha JSON por execução concluída.

3. Em outro terminal, usando `$base` e `$headers` do roteiro local, envie uma consulta:

   ```powershell
   $body = @{ message = 'Quais atendimentos tenho marcados?' } | ConvertTo-Json
   $response = Invoke-WebRequest "$base/v1/agent/messages" -Method Post -Headers $headers `
     -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body))
   $requestId = [string]$response.Headers['X-Request-ID']
   ```

4. Abra a execução correspondente à resposta e ordene suas etapas:

   ```powershell
   $runs = Get-Content traces/agent.jsonl | ForEach-Object { $_ | ConvertFrom-Json }
   $run = $runs | Where-Object request_id -EQ $requestId | Select-Object -Last 1
   $run | ConvertTo-Json -Depth 50
   $run.steps | Sort-Object order | Format-Table order, id, parent_id, kind, tool_name, outcome, duration_ns
   ```

   Entre a aquisição e as gravações da conversa, a consulta normal mostra
   `model → tool(list_appointments) → model → answer`.
   `input` e `output` na execução são a mensagem inicial e a resposta final construída pela aplicação.
   Nas etapas `model`, esses campos mostram o contrato completo de requisição e a resposta estruturada.
   Na etapa `tool`, mostram argumentos e resultado. `parent_id` aponta para a chamada ao modelo que originou a tool;
   `call_id` é o hash SHA-256 do identificador do fornecedor, preservando correlação sem exportar texto arbitrário.
   As mensagens do contrato preservam a ligação entre tool calls e resultados dentro da requisição ao modelo.

## Encontrar uma falha

Uma falha HTTP mantém `X-Request-ID`; o mesmo ID aparece em `error.request_id` e no log `http_request`.
`Invoke-WebRequest` lança uma exceção em respostas HTTP de erro. Para obter o ID dessa resposta,
repita a requisição dentro de `try/catch`:

```powershell
try {
  $response = Invoke-WebRequest "$base/v1/agent/messages" -Method Post -Headers $headers `
    -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body))
  $requestId = [string]$response.Headers['X-Request-ID']
} catch {
  if ($null -eq $_.Exception.Response) { throw }
  $requestId = @($_.Exception.Response.Headers.GetValues('X-Request-ID'))[0]
}
$runs = Get-Content traces/agent.jsonl | ForEach-Object { $_ | ConvertFrom-Json }
$run = $runs | Where-Object request_id -EQ $requestId | Select-Object -Last 1
```

Falhas de conexão sem resposta HTTP não possuem um trace de turno. Para navegar da execução à etapa que falhou:

```powershell
$failed = $run.steps | Where-Object id -EQ $run.failure_step_id
$failed | ConvertTo-Json -Depth 50
$run.steps | Where-Object id -EQ $failed.parent_id | ConvertTo-Json -Depth 50
```

`outcome` distingue `ok`, `error`, `cancelled`, `timeout` e `limit`.
`error_code` contém somente códigos conhecidos da aplicação, `CANCELLED`, `DEADLINE_EXCEEDED` ou `INTERNAL_ERROR`.
Uma etapa `validation` identifica tool calls com formato ou correlação inválidos;
`answer` valida a resposta estruturada.
`conversation.acquire` e `conversation.save` identificam acesso e gravações duráveis sem exportar estado.
`proposal.validate` e `confirmation.recover` identificam validação da proposta e recuperação de confirmação.
Erros de entrada anteriores à aquisição aparecem no resultado da execução, sem etapas de modelo ou ferramentas.
O encerramento após quatro chamadas ao modelo aparece como etapa `limit`, com `AGENT_STEP_LIMIT`.
Cada etapa possui ID, ordem, início, duração em nanossegundos e resultado. A execução também informa sua duração total.

Para reproduzir falhas sem consumir a API ou usar dados reais, execute os cenários determinísticos:

```powershell
go test ./internal/agent -run TestTrace -count=1 -v
go test ./internal/trace -count=1 -v
```

Eles cobrem falha do modelo após uma tool, argumentos inválidos, resposta inválida, cancelamento, timeout,
limite de etapas e falha do arquivo de saída. As saídas são consultadas pelos testes em memória.

## Conteúdo e comportamento

A captura padrão omite conteúdo conversacional. O modo sintético preserva o conteúdo de teste e remove campos de
credenciais, inclusive em JSON dentro de strings, padrões Bearer, chaves `sk-`,
tokens de sessão de 64 dígitos hexadecimais
e valores de credenciais configuradas. A composição do servidor fornece a chave do modelo e as URLs de banco ao redator.
Headers HTTP, identidade autenticada, erros brutos do fornecedor e objetos internos de estado nunca são exportados.
Valores secretos adicionais fornecidos a um modelo alternativo devem entrar em `trace.Options.Secrets` na composição.
O modo sintético não anonimiza dados pessoais arbitrários: mantenha-o desativado com pacientes reais.
Os logs HTTP continuam contendo somente metadados; não recebem conteúdo nem erros brutos das etapas.

O agente mantém a sequência de execução, o acesso exclusivo, as gravações intermediárias e o rollback existentes.
As etapas cercam operações síncronas no mesmo coordenador, sem callbacks nem mudanças nas interfaces do modelo ou tools.
O exportador recebe a execução após liberar o acesso à conversa e funciona mesmo quando o contexto foi cancelado.
Em um panic, o trace encerra as etapas abertas com erro seguro e preserva a propagação original para o caller.
O valor do panic não entra no trace.
Falhas de escrita incrementam `Failures()` e não mudam a resposta ou o estado salvo; o servidor registra a contagem
no encerramento sem incluir a mensagem de erro do sistema de arquivos.
O writer deve retornar prontamente. O arquivo local é serializado entre turnos concorrentes no mesmo processo.
Uma interrupção abrupta do processo pode perder a execução em andamento; esta entrega não implementa coleta remota
nem rotação ou retenção automática do arquivo.
