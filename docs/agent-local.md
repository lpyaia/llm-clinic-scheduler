# Consultas e busca conversacional de horários — tickets #7 e #10

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
   Invoke-RestMethod "$base/v1/agent/messages" -Method Post -Headers $headers `
     -ContentType 'application/json; charset=utf-8' `
     -Body ([System.Text.Encoding]::UTF8.GetBytes($question))
   ```

   Uma conta nova recebe `appointments: []` e uma mensagem informando que não há
   atendimentos para os filtros consultados. Para verificar uma lista preenchida,
   agende um atendimento com essa sessão pelo fluxo **Fluxo completo via HTTP**
   do README e repita a pergunta. Use outra sessão para verificar o isolamento.
   Também é possível testar pela Swagger UI em `/docs`.

4. Perguntas como “Liste meus agendamentos cancelados” usam o filtro de status.
   A ferramenta aceita `status` (`scheduled`, `cancelled`, `completed` ou vazio
   para todos), `from` e `to` (RFC 3339, intervalo `[from,to)`). Valores nulos ou
   ausentes usam `scheduled` e não restringem datas.

5. Para buscar disponibilidade, envie “Quero cardiologia semana que vem à tarde”.
   Guarde o `conversation_id` retornado e inclua-o nas próximas mensagens:

   ```powershell
   $question = @{ message = 'Quero cardiologia semana que vem à tarde' } | ConvertTo-Json
   $reply = Invoke-RestMethod "$base/v1/agent/messages" -Method Post -Headers $headers `
     -ContentType 'application/json; charset=utf-8' `
     -Body ([System.Text.Encoding]::UTF8.GetBytes($question))
   $question = @{ conversation_id = $reply.conversation_id; message = 'Depois de terça' } | ConvertTo-Json
   $reply = Invoke-RestMethod "$base/v1/agent/messages" -Method Post -Headers $headers `
     -ContentType 'application/json; charset=utf-8' `
     -Body ([System.Text.Encoding]::UTF8.GetBytes($question))
   $question = @{ conversation_id = $reply.conversation_id; message = 'Prefiro de manhã' } | ConvertTo-Json
   Invoke-RestMethod "$base/v1/agent/messages" -Method Post -Headers $headers `
     -ContentType 'application/json; charset=utf-8' `
     -Body ([System.Text.Encoding]::UTF8.GetBytes($question))
   ```

   Use nomes presentes no catálogo local (`GET /v1/doctors` e `/v1/specialties`).
   Nomes ausentes, desconhecidos ou ambíguos geram perguntas; as outras preferências
   são preservadas durante o esclarecimento. Campos `null` da ferramenta preservam
   preferências; strings vazias removem a preferência anterior. Mudanças explícitas
   substituem o valor anterior. Não há reserva nem mutação ao buscar horários.

   A ferramenta `find_available_slots` aceita nomes `doctor` e `specialty`,
   `period`, `date`, `from` e `to`; não aceita IDs de paciente ou de catálogo.
   O modelo traduz português para esses argumentos. O servidor converte `today`,
   `tomorrow`, `this_week`, `next_week`, `after_monday` até `after_sunday` e
   `earliest` usando o relógio injetado e o fuso da clínica. Semanas são de segunda
   a segunda (fim exclusivo); “depois de terça” começa na quarta-feira, usando a
   semana já escolhida ou a próxima ocorrência futura. Refinamentos mantêm os
   limites explícitos; trocar o dia da semana substitui o refinamento anterior
   dentro desses limites. `earliest` remove limites
   de data para buscar a primeira janela com disponibilidade. Datas explícitas usam
   `from`/`to` RFC 3339. Preferências de data são armazenadas como instantes, sem
   reinterpretar “semana que vem” a cada mensagem.

   O módulo de preferências em `internal/agent/preferences.go` concentra atualização,
   limpeza, esclarecimento e resolução de datas. A busca recebe os critérios resolvidos
   e mantém a resolução de nomes no catálogo. O contexto JSON enviado ao modelo preserva
   o formato existente; o armazenamento da conversa confirma preferências e histórico
   juntos após o sucesso da interação.

   `application.Service.FindSlots` mantém a política existente: sem fim explícito,
   busca duas semanas e amplia até oito; com fim explícito, não amplia. O objeto
   `availability` contém apenas slots reais, `searched_from`, `searched_until` e
   `expanded`. A mensagem também informa o intervalo e a ausência de resultados.
   A busca termina na ferramenta e o servidor monta a resposta. Para iniciar outra
   conversa, omita o ID.

## Execução e proteção dos resultados

`agent.Model` recebe mensagens, definições de ferramentas e, nas continuações, um
schema de resposta com nome e conteúdo JSON. A primeira chamada de cada mensagem
usa `RequireTool`: exige a seleção de uma ferramenta e omite o schema final.
O adapter envia `tool_choice: "required"` nessa chamada, sem `response_format`.
Uma continuação permite novas ferramentas ou a resposta final no schema fornecido
pelo módulo ativo. Tipos OpenAI ficam no adapter. Trocar esse adapter por um modelo
simulado não altera o Scheduling Core.

As duas ferramentas implementam a mesma interface interna `toolExecutor`, definida
em `internal/agent/tools.go`. Cada módulo fornece a definição da ferramenta e executa
seus argumentos. As instâncias são criadas para cada mensagem, com o paciente
autenticado ou as preferências da conversa já vinculados. A execução produz um de
dois resultados:

- `returnToModel` (retornar ao modelo): dados, schema nomeado, instruções de resposta
  e um finalizador que verifica a resposta posterior. `list_appointments`, em
  `appointments.go`, fornece `appointment_answer` com os IDs da última consulta.
- `replyToPatient` (responder ao paciente): resposta pronta que encerra a mensagem.
  `find_available_slots`, em `search.go`, usa esse resultado para disponibilidade e
  esclarecimentos. A conversa pode continuar em uma nova mensagem.

O loop encaminha o schema e as instruções de `returnToModel` à próxima chamada.
O adapter serializa o nome e o conteúdo recebidos com `strict: true`. A definição
JSON continua em `internal/agent/schemas/appointment_answer.json`, embutida por
`schemas.go`. A busca de horários responde ao paciente durante a seleção de tool.

O loop aceita uma chamada de ferramenta por etapa e no máximo quatro chamadas ao
modelo. Controla cancelamento, nome, ID e correlação entre chamadas e resultados.
Os módulos validam argumentos, campos desconhecidos, chaves duplicadas, status e
intervalo antes de acessar o núcleo. Respostas finais sem consulta prévia, chamadas
repetidas com o mesmo ID, respostas incompletas, recusas e respostas misturando
conteúdo final e ferramentas são rejeitadas.

O modelo devolve `appointment_ids` com todos os IDs da última consulta, na mesma
ordem. O servidor verifica essa lista e monta a mensagem em português com os
dados reais, inclusive a ausência de resultados. O resultado da ferramenta e a
resposta HTTP omitem identidade, telefone e outros campos desnecessários.

O módulo de consulta conserva apenas a última lista obtida durante a execução da
mensagem. Uma consulta posterior substitui essa lista, inclusive quando retorna
vazia. O finalizador e os resultados são descartados ao encerrar a mensagem; uma
nova mensagem exige uma nova consulta. O loop permite trocar de listagem para busca
de horários, que encerra a mensagem com sua própria resposta.

`agent.ConversationStore` abstrai armazenamento e atualização atômica de estado e
histórico. A implementação em memória isola por ID e paciente, serializa mensagens
da mesma conversa, respeita cancelamento ao esperar e só grava alterações após
sucesso. Conversas distintas podem avançar em paralelo. O histórico conserva as
últimas dez interações completas; preferências estruturadas permanecem. Reiniciar
o processo perde as conversas, e IDs antigos retornam `NOT_FOUND` (404). Esta fase
não oferece memória compartilhada entre instâncias nem persistência em banco.

O timeout HTTP configurado se
propaga ao modelo e ao banco; o cliente do fornecedor também limita cada chamada
a 30 segundos, inclusive quando o timeout HTTP está desabilitado para debug.
Respostas do fornecedor têm limite de 1 MiB e 2048 tokens de saída. O adapter
envia `store: false`. A mensagem do paciente e os dados da consulta são enviados
ao fornecedor configurado; use dados fictícios no roteiro local. Logs HTTP não
registram esse conteúdo nem a chave. Uma busca de horários usa uma chamada de API;
consultar agendamentos normalmente usa duas. Ambas estão sujeitas à cobrança da
conta do fornecedor.

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
