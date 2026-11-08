# Fluxos conversacionais de agendamento — tickets #7, #10, #11, #12 e #13

O paciente autenticado consulta seus agendamentos em português por
`POST /v1/agent/messages`. A ferramenta `list_appointments` reutiliza
`application.Service.ListAppointments`; somente o middleware de sessão fornece
a identidade. Nem o corpo HTTP nem os argumentos da ferramenta aceitam
`patient_id`. A ferramenta `create_appointment` prepara uma proposta pelo mesmo
núcleo; a aplicação exige confirmação explícita em uma mensagem posterior.

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
   `period`, `date`, `from`, `to` e `clarification_question`; não aceita IDs de paciente ou de catálogo.
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

   O modelo recebe a data atual e o fuso da clínica. “Na quarta” seleciona somente
   aquele dia por `from`/`to`, com fim exclusivo à meia-noite seguinte; “depois de quarta”
   usa `after_wednesday`. A semana já escolhida determina o dia quando inequívoca;
   sem essa referência, vale a próxima ocorrência futura. No próprio dia, “na quarta”
   pode incluir hoje; “próxima quarta” seleciona a ocorrência após hoje.
   Horas específicas usam `from`/`to` no dia escolhido, `date=null` e `period=""`,
   removendo um período amplo anterior que poderia contrariar o novo horário.

   `date=clarify` exige `clarification_question` não vazia, perguntando somente pelo
   ponto ambíguo que o contexto não resolve. Sem esclarecimento, esse campo deve ser
   `null`. A pergunta é devolvida ao paciente sem consultar disponibilidade e não é
   armazenada como preferência. Preferências válidas de médico, especialidade e período
   são mantidas; a pergunta e a resposta ficam no histórico. Enquanto a data é esclarecida,
   o intervalo anterior continua salvo. Uma chamada inválida não altera as preferências.

   O módulo de preferências em `internal/agent/tools/preferences.go` concentra atualização,
   limpeza, esclarecimento e resolução de datas. A busca recebe os critérios resolvidos
   e mantém a resolução de nomes no catálogo. O contexto JSON enviado ao modelo preserva
   o formato existente; o fluxo grava preferências e histórico
   juntos após o sucesso da interação.

   `application.Service.FindSlots` mantém a política existente: sem fim explícito,
   busca duas semanas e amplia até oito; com fim explícito, não amplia. O objeto
   `availability` contém apenas slots reais, `searched_from`, `searched_until` e
   `expanded`. A mensagem também informa o intervalo e a ausência de resultados.
   A busca termina na ferramenta e o servidor monta a resposta. Para iniciar outra
   conversa, omita o ID.

## Conferir a interpretação de datas

Os testes com modelo simulado verificam os argumentos e seus efeitos, sem medir interpretação em português.
Para avaliar o provedor real, confira os argumentos de `find_available_slots` com um relógio controlado.
Referência para os casos abaixo: `2026-11-06T19:00:00-03:00`, fuso `America/Sao_Paulo`, cardiologia já escolhida.
Os campos não alterados devem ser `null`; `clarification_question` deve ser `null` nas buscas sem ambiguidade.

| Mensagem | Argumentos temporais esperados |
| --- | --- |
| Na quarta-feira | `date=null`, `from=2026-11-11T00:00:00-03:00`, `to=2026-11-12T00:00:00-03:00` |
| Quarta, 10h–12h | `date=null`, `period=""`, `from=2026-11-11T10:00:00-03:00`, `to=2026-11-11T12:00:00-03:00` |
| Depois de quarta, semana que vem | Após escolher `next_week`, `after_wednesday` busca de 12/11 até 16/11 (exclusivo) |
| Quarta, às 10 ou às 22, ainda não decidi | `date=clarify`; pergunta somente qual dos dois horários deseja |

Repita o pedido de 10h–12h após escolher a tarde: `period=""` deve remover esse filtro.
Com `Agora na clínica=2026-11-07T00:30:00Z` convertido para São Paulo, “hoje” ainda é 06/11.
Com `Agora na clínica=2026-12-31T19:00:00-03:00`, “próxima quarta” é 06/01/2027.

## Agendar com confirmação

Depois de uma busca, continue com o mesmo `conversation_id`: “Quero o primeiro
horário para consulta”. Categorias aceitas são consulta, exame e procedimento.
O modelo escolhe somente entre os IDs dos horários apresentados na última busca.
Uma escolha incompleta gera pergunta; a conversa guarda o horário ou categoria
já informados. Nomes, horários e categorias ambíguos exigem esclarecimento.

`create_appointment` chama `application.Service.Prepare` com a identidade da sessão.
Não reserva o horário. A resposta inclui `pending_action` com `id`, `summary`,
`category` e `expires_at`, além de uma mensagem com categoria, resumo, validade
no fuso da clínica e pedido explícito de confirmação. A ação vale dez minutos.

Após ler o resumo, responda na mesma conversa: por exemplo, “Aham, pode marcar”,
“confirmado” ou “aceito”. O modelo interpreta a mensagem atual no contexto da proposta,
sem listas fixas de expressões. `respond_to_proposal` recebe somente `decision`:
`confirm`, `reject` ou `clarify`. Não aceita ID de ação, paciente ou revisão.
Perguntas, condições e ambiguidades devem gerar `clarify`; recusas, inclusive
indiretas como “Melhor deixar para depois”, usam `reject`.

Alterações usam as tools existentes: “Sim, mas prefiro exame” chama
`create_appointment` com a nova categoria, apresenta outro resumo e exige mensagem
posterior. Uma nova busca usa `find_available_slots`. Preparar encerra o turno:
nenhuma continuação ou segunda chamada ao modelo pode confirmar uma proposta
criada nesse mesmo turno. A decisão e comparação das alternativas estão em
[Respostas contextuais a propostas](adr/0001-contextual-proposal-responses.md).

A aplicação vincula ação, paciente e revisão antes de chamar o modelo. Somente
uma proposta já apresentada ou referência à ação concluída habilita a tool de
resposta. O núcleo revalida ownership, validade, disponibilidade e limite semanal
na confirmação. Sem proposta, o modelo deve pedir uma escolha; “não” não descarta
uma proposta inexistente. Conversas alheias retornam `NOT_FOUND` (404).

Toda mensagem concorrente agora retorna `INVALID_INPUT` (400) imediatamente,
antes da interpretação. Aguarde a resposta, leia o resumo e reenvie a mensagem.
Antes, apenas confirmações reconhecidas pela lista tinham essa proteção; demais
mensagens esperavam. Conversas diferentes continuam independentes.

A autoridade anterior é revogada e gravada antes da interpretação. Rejeição,
alteração, ambiguidade, desvio de intenção, falha do modelo, resposta inválida ou
cancelamento não permitem confirmar depois a proposta anterior. Para retomar,
escolha ou altere horário/categoria e leia o novo resumo antes de confirmar.
Trocar apenas categoria preserva o horário escolhido; nova busca limpa escolhas
e horários apresentados. `null` preserva escolhas; string vazia limpa um campo ambíguo.

Proposta expirada retorna `ACTION_EXPIRED` (410) antes da interpretação e perde
a autoridade; é necessária nova preparação. Se expirar durante a chamada ao
modelo, o núcleo também impede a reserva. Reenvios bem-sucedidos usam a referência
à ação concluída e retornam o mesmo agendamento em `appointments`, inclusive após
a validade original. Revogar essa referência nunca cancela o agendamento.

## Execução e proteção dos resultados

`agent.Model` recebe mensagens, definições de ferramentas e, nas continuações, um
schema de resposta com nome e conteúdo JSON. A primeira chamada de cada mensagem
usa `RequireTool`: exige a seleção de uma ferramenta e omite o schema final.
O adapter envia `tool_choice: "required"` nessa chamada, sem `response_format`.
Uma continuação permite novas ferramentas ou a resposta final no schema fornecido
pelo módulo ativo. Tipos OpenAI ficam no adapter. Trocar esse adapter por um modelo
simulado não altera o Scheduling Core.

A interface `tools.Executor`, definida em `internal/agent/tools/tools.go`, fornece
as definições e executa os argumentos das ferramentas. Consulta, busca e preparação
ficam no package `tools`; nenhuma delas conhece `Agent`, `conversationFlow` ou o
armazenamento da conversa. As instâncias recebem apenas o núcleo e os schemas.
Cada execução recebe `tools.Input`: identidade autenticada pela aplicação,
preferências por valor, seleção por valor e uma cópia dos horários apresentados.
Argumentos do modelo nunca escolhem a identidade. A execução produz um de três resultados:

- `tools.ReturnToModel` (retornar ao modelo): dados, schema nomeado, instruções de resposta
  e um finalizador que verifica a resposta posterior. `list_appointments`, em
  `appointments.go`, fornece `appointment_answer` com os IDs da última consulta.
- `tools.ReplyToPatient` (responder ao paciente): resposta pronta que encerra a mensagem.
  `find_available_slots`, em `search.go`, usa esse resultado para disponibilidade e
  esclarecimentos. `create_appointment`, em `booking.go`, apresenta a proposta ou
  esclarecimento sem outra chamada ao modelo. A conversa pode continuar em uma nova mensagem.
- `tools.ProposalResponse`: interpretação de confirmação, rejeição ou esclarecimento,
  sem executar mutação. O fluxo verifica o vínculo e somente então chama o núcleo.

O fluxo aplica os valores retornados em `Search` e `Booking`, incluindo escolhas
incompletas e preferências preservadas durante esclarecimentos. A busca refina uma
cópia local; a preparação valida a seleção contra `Input.Offered`, sem acessar a
implementação da busca. Nenhuma ferramenta recebe ponteiros para o estado da conversa.
Em erro, não há atualização retornada para aplicar. A gravação continua a cargo do turno.

O loop encaminha o schema e as instruções de `tools.ReturnToModel` à próxima chamada.
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

Cada execução da consulta devolve um finalizador que captura somente os seus
resultados, sem I/O nem retorno ao fluxo ou à ferramenta. O loop conserva apenas o
último finalizador da mensagem. Uma consulta posterior substitui essa lista,
inclusive quando retorna vazia. O finalizador e os resultados são descartados ao encerrar a mensagem; uma
nova mensagem exige uma nova consulta. O loop permite trocar de listagem para busca
de horários, que encerra a mensagem com sua própria resposta.

### Mapa de dependências

```text
agent (entrada autenticada, fluxo, armazenamento e loop)
  -> tools (consulta, busca, preparação, interpretação de proposta e apresentação)
  -> contract (mensagens, dados apresentados e validação JSON)
tools -> contract, application, domain
contract -> domain (erro de resposta inválida)
application -> domain
```

`contract` define mensagens e dados compartilhados sem importar a implementação do
agente. `agent` mantém aliases para preservar os tipos usados pelo HTTP e pelo adapter
do modelo; não há implementações paralelas. `SearchPreferences`, em
`tools/preferences.go`, é um valor opaco com zero value utilizável, independente de
qualquer instância de busca. O fluxo guarda esse valor e aplica apenas os valores
devolvidos. `contract.DecodeObject` é a única implementação de validação de objetos,
campos desconhecidos e chaves duplicadas, usada nos argumentos e nas respostas finais.
`tools/presentation.go` reúne esclarecimentos, nomes de categorias e apresentação
de agendamentos, reutilizada também na confirmação pelo fluxo.

Os schemas de produção continuam em `internal/agent/schemas/*.json`, embutidos
exclusivamente por `internal/agent/schemas.go` e fornecidos às ferramentas na composição.
Tipos específicos do fornecedor continuam no adapter.

O módulo de fluxo (`internal/agent/flow.go` e `booking.go`) é dono de `Conversation`
e de suas transições. `Agent.ReplyConversation` coordena o turno em ordem: adquire
acesso exclusivo imediato, captura o vínculo anterior, grava a revogação, valida
a referência no núcleo, executa o loop, grava a resposta e libera acesso por `defer`.
O acesso permanece exclusivo durante chamadas ao modelo e núcleo. O fluxo não
recebe callbacks nem reentra no agente. Uma decisão estruturada retorna ao fluxo,
que valida identidade e revisão antes de executar a confirmação determinística.

O estado distingue horários apresentados (`Offered`), seleção incompleta
(`Selection`), proposta aguardando resposta (`Proposal`), referência concluída
(`Completed`) e revisão (`Revision`). A revisão avança ao consumir uma referência
e ao apresentar nova proposta. O vínculo é local ao turno; após desvio para outra
tool, `respond_to_proposal` também é removida das próximas chamadas ao modelo.
Uma preparação retorna imediatamente outro resumo, sem autoridade para confirmá-lo
no mesmo turno. O armazenamento apenas valida ownership, exclusão e gravação.

`ConversationStore.Acquire` entrega `LockedConversation`. O agente sempre usa
`Wait: false`; ocupação vira `INVALID_INPUT`. `CreateConversation` permite um ID
novo; follow-ups desconhecidos não criam conversas. `Snapshot` e `Save` copiam o
estado. Cada gravação é durável durante o processo; `Save` rejeita contextos
cancelados. Uma conversa nova sem gravação é removida ao liberar o acesso.

Revogação anterior à interpretação permanece mesmo após falha ou cancelamento;
as demais alterações, preferências e histórico só são salvos após resposta válida.
Uma busca malsucedida preserva escolhas anteriores, mas exige nova preparação.
Antes, confirmações reconhecidas preservavam autoridade após falha; agora a
interpretação malsucedida também a consome. Confirmações expiradas deixam de
repetir `ACTION_EXPIRED`: a primeira tentativa revoga a referência, e as próximas
exigem nova preparação. Validade e ownership continuam protegidos pelo núcleo.

Se o núcleo confirmar e a gravação final falhar ou for cancelada, o agendamento
permanece, mas a referência conversacional já foi revogada. Consulte a listagem
para verificar o resultado; a recuperação ampliada fica em #14. Preparar sem
conseguir gravar a resposta nunca concede autoridade para confirmar a ação.

O histórico conserva as últimas dez interações completas; preferências estruturadas
permanecem. Reiniciar o processo perde as conversas, e IDs antigos retornam
`NOT_FOUND` (404). Esta fase não oferece memória compartilhada entre instâncias
nem persistência de conversas em banco.

O timeout HTTP configurado se
propaga ao modelo e ao banco; o cliente do fornecedor também limita cada chamada
a 30 segundos, inclusive quando o timeout HTTP está desabilitado para debug.
Respostas do fornecedor têm limite de 1 MiB e 2048 tokens de saída. O adapter
envia `store: false`. A mensagem do paciente e os dados da consulta são enviados
ao fornecedor configurado; use dados fictícios no roteiro local. Logs HTTP não
registram esse conteúdo nem a chave. Uma busca de horários ou preparação usa uma chamada de API;
consultar agendamentos normalmente usa duas. Essas chamadas estão sujeitas à cobrança da
conta do fornecedor. Interpretar uma resposta à proposta usa uma chamada ao fornecedor.

Erros mantêm o envelope HTTP existente: `INVALID_INPUT` (400), `UNAUTHORIZED`
(401), `MODEL_INVALID_RESPONSE` ou `AGENT_STEP_LIMIT` (502), `AGENT_UNAVAILABLE`
(503). Falhas de transporte/fornecedor e cancelamentos retornam `INTERNAL_ERROR`
(500), sem detalhes internos. Sem chave, os outros endpoints continuam disponíveis.

## Remarcação por conversa (#12)

Use o mesmo `conversation_id` nas mensagens posteriores. Exemplo com um exame já agendado:

1. “Quero remarcar meu exame com a Ana.” `select_appointment` consulta somente os agendamentos
   próprios, futuros e ativos. Aceita `appointment_id`, `doctor`, `category`, `from` e `to`;
   os filtros descrevem o atendimento original. Datas usam RFC3339 e intervalo `[from,to)`.
   Se houver vários candidatos, a resposta apresenta a lista e pede qual deseja. O modelo recebe
   os IDs verificados dos candidatos no próximo turno. `appointment_id` vazio força esclarecimento,
   inclusive quando resta apenas um candidato; nenhum resultado autoriza selecionar uma reserva alheia.
2. “O de quarta-feira.” Após identificar uma única reserva, a resposta apresenta seus dados e pede
   o destino. “Mostre os horários da próxima semana” usa `find_available_slots`, conservando o
   ID do médico original, inclusive quando existem médicos com nomes iguais. O horário original
   não é uma opção de destino. A busca apresenta somente horários reais e mantém as políticas do núcleo.
3. “Quero o primeiro, pode remarcar.” `reschedule_appointment` aceita somente `slot_id`, de um
   horário apresentado. Um ID ausente ou ambíguo pede esclarecimento. A ferramenta prepara a ação
   `reschedule_appointment` no núcleo, sem mudar a reserva. O resumo mostra data original, destino,
   médico, categoria e validade. A categoria exibida vem do atendimento original; na ação armazenada
   permanece nula, conforme o contrato do banco para remarcação.
4. “Sim, confirmo a remarcação.” `respond_to_proposal` interpreta a mensagem atual. Somente
   `confirm`, vinculado à ação apresentada em turno anterior, chama `Service.Confirm`.
   O núcleo preserva ID, médico e categoria e altera o horário em uma transação. Reenviar a
   confirmação utiliza a mesma ação concluída, sem executar outra alteração.

Recusa, pergunta, condição ou ambiguidade não executam a remarcação e consomem a proposta.
Para escolher outro destino, prepare outro resumo antes de confirmar. A mensagem de recusa informa
que o original foi mantido. Se a proposta já tiver sido concluída, descartar sua referência de
reenvio também não desfaz a alteração realizada.

“Prefiro com o Bruno” inicia a intenção de novo agendamento, explica que o original será mantido e
busca horários do novo médico na mesma execução. “Prefiro consulta em vez de exame”
usa `create_appointment` e prepara um novo atendimento, que exige seu próprio resumo e confirmação.
Esses caminhos nunca cancelam nem alteram automaticamente o atendimento original.

O fluxo guarda separadamente os candidatos próprios (`Candidates`) e o original selecionado
(`Rescheduling`). Selecionar outro original limpa horários e propostas anteriores; buscar destinos
limpa a seleção de slot e a autoridade anterior, conservando o original. As ferramentas devolvem
valores explícitos, aplicados pelo fluxo; não acessam o armazenamento da conversa.

O agente conserva o acesso exclusivo à conversa durante todo o turno. Antes de interpretar uma
resposta, grava a revogação da autoridade anterior. Na confirmação, o núcleo trava paciente, ação,
agendamento e destino, revalida ownership, versão, validade, disponibilidade e limite semanal e
grava a alteração atomicamente. Um conflito de destino ou falha de escrita preserva a reserva
original e não produz mensagem de sucesso. A transação interna reverte uma escrita malsucedida;
o armazenamento da conversa continua com a proposta revogada. Os limites de recuperação após
sucesso do núcleo e falha da gravação da conversa permanecem os descritos acima, com ampliação em #14.

Os testes em `internal/agent/rescheduling_test.go` exercitam a interface pública do agente e
consultam os efeitos pelas operações públicas do núcleo. Cobrem ambiguidade e isolamento,
filtros do original, médicos homônimos, preparação sem alteração, confirmação e reenvio,
recusa, esclarecimento, troca de médico/categoria, destinos não apresentados, conflito,
expiração, mudança concorrente do original e rollback após falha de escrita.
O teste `TestConversationalReschedulingPostgres` também prepara e confirma propostas pelo agente
com o banco real, cobrindo reenvio e conflito de destino, inclusive o contrato de categoria nula da ação.
O modelo simulado valida o protocolo e seus efeitos; a interpretação real em português continua
sujeita à verificação com o fornecedor configurado.

## Cancelamento por conversa (#13)

Use o mesmo `conversation_id` para selecionar e confirmar o atendimento:

1. “Quero cancelar minha consulta.” `cancel_appointment` consulta os atendimentos próprios,
   ativos e futuros. Compartilha os filtros `appointment_id`, `doctor`, `category`, `from` e `to`
   de `select_appointment`, incluindo intervalo `[from,to)` e datas RFC3339 no fuso da clínica.
   Vários candidatos são apresentados sem preparar ação. No próximo turno, o modelo recebe
   seus IDs verificados para identificar a escolha; um ID vazio exige esclarecimento mesmo
   com apenas um candidato. IDs desconhecidos ou alheios nunca selecionam outro atendimento.
2. “O exame de quarta com a Ana.” Uma escolha única prepara `cancel_appointment` no núcleo
   e apresenta médico, data, categoria, intenção de cancelar e validade. A categoria exibida
   vem do original; na ação armazenada permanece nula. Mesmo “cancele, confirmo” nesta mensagem
   apenas prepara o resumo. Nenhuma reserva é alterada nesta etapa.
3. “Sim, confirmo o cancelamento.” `respond_to_proposal` interpreta a mensagem atual e a aplicação
   confirma somente a ação vinculada ao resumo anterior. A resposta apresenta o atendimento
   retornado pelo núcleo com status `cancelled`. Reenvios usam a mesma ação concluída, inclusive
   após sua validade original, sem incrementar a versão ou cancelar outro atendimento.

Recusa ou ambiguidade consomem a autoridade e informam que o atendimento foi mantido.
Uma confirmação posterior exige outra preparação e resumo. Trocar o atendimento a cancelar
também prepara outra proposta, sem executar a anterior. Recusar a referência a um cancelamento
já concluído não restaura a reserva; a mensagem informa que o cancelamento foi mantido.

`cancel_appointment` tem executor próprio em `tools/cancellation.go` e schema próprio em
`schemas/cancel_appointment.json`. `select_appointment` identifica somente o original de remarcação.
As tools reutilizam apenas a validação dos filtros e a consulta de candidatos elegíveis,
em `tools/appointment_candidates.go`; cada uma constrói sua resposta e suas atualizações.
O cancelamento prepara a ação após a escolha única, sem busca de destino. O fluxo aplica os valores retornados,
limpa horários e seleção anteriores, guarda os candidatos ou a proposta e remove o original
de remarcação para impedir a reutilização dessa intenção.

O agente mantém o acesso exclusivo durante o turno e grava a revogação antes da interpretação,
como nos outros tipos de proposta. `Service.Confirm` trava paciente, ação e atendimento,
revalida ownership, versão, início, estado e expiração e grava o cancelamento atomicamente.
Uma falha de escrita reverte a alteração; uma falha de confirmação não produz sucesso e
exige nova proposta. A gravação final da conversa ocorre após o resultado do núcleo;
as limitações de recuperação de #14 continuam aplicáveis.

Os testes em `internal/agent/cancellation_test.go` usam `Agent.ReplyConversation` e verificam
os efeitos por `Service.ListAppointments` e `Service.GetAction`. Cobrem resumo sem mutação,
confirmação, reenvio após validade, recusa, ambiguidade, troca de alvo, isolamento, inelegibilidade,
argumentos inválidos, expiração, alteração concorrente e rollback. O modelo é simulado;
a qualidade da interpretação em português com o fornecedor real não é medida por esses testes.

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
