# Plano de desenvolvimento — Go, Fiber e GORM

Plano aprovado em 16/10/2026, derivado do [PRD original](prd.md). Este documento registra as decisões de desenvolvimento; as instruções de execução e o estado da entrega inicial estão no [README](../README.md).

## 1. Objetivo e roadmap

Construir incrementalmente um sistema em que o backend garante as regras de agendamento e o agente interpreta solicitações e conduz a conversa. A primeira entrega será uma API REST sem IA, utilizando **Fiber e GORM**.

| Fase | Entrega | Critério de conclusão |
|---|---|---|
| **1. Núcleo determinístico** | Catálogo, pacientes, disponibilidade, confirmação e agendamentos | Fluxos completos via HTTP, com garantias de autorização e concorrência |
| **2. Agente conversacional** | Abstração de LLM, tools, loop limitado e estado em memória | Conversas em português operam o núcleo e preservam contexto |
| **3. Observabilidade e avaliações** | Traces, métricas e dataset de conversas | Execuções podem ser investigadas e comparadas |
| **4. RAG operacional** | Ingestão, embeddings, pgvector, recuperação e citações | Respostas fundamentadas nos documentos da clínica, com abstention |
| **5. Avaliações de RAG** | Dataset e métricas de recuperação e respostas | Estratégias de chunking e top-k podem ser comparadas |
| **6. Conversas persistentes** | Mensagens e estado no PostgreSQL | Reinícios preservam contexto e mensagens concorrentes são serializadas |
| **7. WhatsApp** | Webhook, identificação por telefone e respostas | O canal reutiliza a aplicação e não duplica operações |

A implementação imediata cobre a fase 1.

## 2. Arquitetura e persistência

**Stack:** Go, [Fiber v3](https://docs.gofiber.io/), GORM com o [driver oficial de PostgreSQL](https://gorm.io/docs/connecting_to_the_database.html), PostgreSQL 17, goose e Docker Compose. pgvector será introduzido na fase de RAG.

Adotar um monólito organizado em camadas:

- **Domínio:** entidades, estados e regras puras.
- **Aplicação:** casos de uso, autorização, confirmação e coordenação transacional.
- **Infraestrutura:** repositórios GORM, PostgreSQL, sessões, migrations e relógio.
- **Adapters:** handlers e middlewares Fiber; futuramente agente e WhatsApp.

**Uso de Fiber:**

- Rotas agrupadas em `/v1`, com middlewares de sessão, identificação de requisição, logging e recuperação de panic.
- Tratamento centralizado de erros e DTOs explícitos para entrada e saída.
- A aplicação recebe `context.Context` e parâmetros próprios; `fiber.Ctx` fica restrito ao adapter HTTP.
- Habilitar `Immutable` para evitar retenção de valores reutilizados entre requisições. Esse comportamento está documentado pelo [Fiber](https://docs.gofiber.io/).

**Uso de GORM:**

- Modelos de persistência separados das entidades de domínio; tags GORM e `*gorm.DB` ficam na infraestrutura.
- Consultas e gravações implementadas pelos repositórios GORM.
- Uma abstração transacional fornece repositórios vinculados à mesma transação.
- Regras de negócio ficam nos casos de uso e no domínio; hooks do ORM não executarão essas regras.
- Cancelamento será mudança explícita de status, preservando o registro.
- Migrations SQL versionadas com goose serão a fonte do schema. Não executar `AutoMigrate` na inicialização.
- Índices parciais, checks e outras garantias específicas do PostgreSQL serão definidos nas migrations.

**Modelo inicial:**

| Entidade | Dados e restrições principais |
|---|---|
| Paciente | UUID, nome completo e telefone único em E.164 |
| Sessão local | Paciente, hash do token e expiração |
| Especialidade | UUID e nome único |
| Médico | UUID, nome e uma especialidade |
| Slot | UUID, médico e início; duração de 30 minutos |
| Agendamento | UUID, paciente, slot, categoria, status e versão |
| Ação pendente | Paciente, operação, parâmetros, versão esperada, expiração e resultado |

Categorias: `consultation`, `exam` e `procedure`. Inicialmente, qualquer médico poderá receber as três categorias.

Slots começarão em hora cheia ou meia hora, com unicidade por médico e início. Médico e horário do agendamento serão obtidos pelo slot associado.

Um comando local de seed criará médicos, especialidades e slots simulados relativos à data de execução. Não haverá interface administrativa.

## 3. Contratos e regras da fase 1

### API REST

| Método e rota | Finalidade |
|---|---|
| `POST /v1/dev/sessions` | Identificar ou cadastrar paciente e emitir sessão |
| `GET /v1/specialties` | Listar especialidades |
| `GET /v1/doctors` | Listar médicos, filtrando por especialidade |
| `GET /v1/slots` | Buscar disponibilidade |
| `GET /v1/appointments` | Listar agendamentos próprios, filtrando por status e período |
| `POST /v1/pending-actions` | Preparar criação, remarcação ou cancelamento |
| `GET /v1/pending-actions/{id}` | Consultar resumo e resultado de uma ação própria |
| `POST /v1/pending-actions/{id}/confirm` | Confirmar e executar |
| `GET /health/live` e `/health/ready` | Verificar processo e banco |

A sessão local terá token opaco com validade de 24 horas, enviado como Bearer token. Nome será obrigatório no cadastro; telefone existente localizará o paciente sem alterar seu nome.

Todas as rotas de negócio exigirão sessão. O paciente será obtido do contexto autenticado, nunca de um `patient_id` enviado na operação. O contexto reservará `identity_verified`, inicialmente `false`.

### Disponibilidade

- Filtrar por médico, especialidade ou interseção de ambos.
- Aceitar início, fim opcional e período do dia.
- Sem início, considerar o instante atual.
- Sem fim, buscar duas semanas e ampliar uma semana por tentativa até oito, interrompendo quando encontrar resultados.
- Com fim explícito, respeitar o intervalo solicitado; nenhuma busca excederá oito semanas.
- Retornar slots futuros e livres, ordenados cronologicamente, informando o intervalo pesquisado.
- Períodos no fuso da clínica: manhã `[06h,12h)`, tarde `[12h,18h)` e noite `[18h,24h)`.

### Confirmação

A preparação receberá:

- `create_appointment`: slot e categoria.
- `reschedule_appointment`: agendamento e novo slot.
- `cancel_appointment`: agendamento.

O servidor validará as condições atuais, armazenará a ação por **10 minutos** e devolverá ID, resumo e expiração. Preparar uma ação não reservará o slot.

A confirmação receberá somente seu ID, recuperará os parâmetros armazenados e revalidará todas as regras:

- Confirmações concluídas serão idempotentes.
- Ações expiradas ou baseadas em versão antiga não serão executadas.
- Conflitos de negócio exigirão nova preparação.
- Falhas transitórias permitirão repetir a confirmação.
- Remarcação preservará médico e categoria e manterá o slot original se a operação falhar.
- Cancelamento liberará o slot e preservará o histórico.
- Somente agendamentos `scheduled` ainda não iniciados poderão ser cancelados ou remarcados.
- Um comando local marcará como `completed` os agendamentos cujo término já passou.

Erros terão códigos estáveis e mensagens em português: `SLOT_UNAVAILABLE`, `WEEKLY_LIMIT_REACHED`, `ACTION_EXPIRED` e `APPOINTMENT_CHANGED`. Usar HTTP `400`, `401`, `404`, `409` e `410` conforme entrada inválida, sessão inválida, recurso inexistente ou alheio, conflito e expiração.

### Concorrência

Executar cada confirmação em uma transação explícita com [`db.Transaction`](https://gorm.io/docs/transactions.html). Todos os repositórios envolvidos deverão utilizar o mesmo objeto transacional.

Bloquear a linha do paciente antes de contar os agendamentos da semana, usando o suporte a [`FOR UPDATE` do GORM](https://gorm.io/docs/advanced_query.html). Todas as mutações seguirão essa disciplina.

Criar índice único parcial sobre `slot_id` para agendamentos `scheduled` ou `completed`. A garantia final de exclusividade permanecerá no PostgreSQL.

A alteração do agendamento e o registro do resultado da ação ocorrerão na mesma transação. Remarcações serão atômicas; versionamento e bloqueios protegerão contra alterações baseadas em estado antigo.

## 4. Incrementos e testes

| Incremento | Entrega verificável |
|---|---|
| **1. Fundação** | Go, Fiber, GORM, Compose, migrations, configuração e health checks |
| **2. Identificação e catálogo** | Pacientes, sessões, médicos, especialidades e seed |
| **3. Disponibilidade** | Filtros, períodos e expansão da busca |
| **4. Criação confirmada** | Ações pendentes, limite semanal e reserva transacional |
| **5. Gestão** | Listagem, remarcação, cancelamento e conclusão por comando |
| **6. Fechamento** | Testes concorrentes, OpenAPI e roteiro completo via HTTP |

**Testes unitários:** fronteiras semanais, períodos, expansão, expiração, transições e validações de remarcação.

**Testes HTTP com Fiber:** binding, validação, sessão, autorização e tradução dos erros para o contrato REST.

**Integração com PostgreSQL real:**

- Dois pacientes confirmando o mesmo slot: exatamente um sucesso.
- Paciente com duas reservas confirmando duas novas simultaneamente: apenas uma criada.
- Remarcação na mesma semana sem contar o próprio agendamento duas vezes.
- Destino ocupado ou semana cheia preservando a reserva original.
- Cancelamento liberando slot e limite semanal.
- Acesso a recursos de outro paciente.
- Confirmação repetida, concorrente, expirada ou desatualizada.
- Reinício preservando sessões, ações e resultados.
- Respeito ao período explícito e ao limite de oito semanas.
- Rollback integral quando uma operação transacional falhar.

Critério de aceite: fluxo completo pelo contrato HTTP e aprovação das suítes unitária, HTTP e de integração. Os testes de concorrência utilizarão PostgreSQL, reproduzindo os índices e bloqueios reais.

## 5. Premissas e evolução

- Fuso: `America/Sao_Paulo`; armazenamento com `timestamptz`.
- Semana de segunda a domingo; `scheduled` e `completed` contam no máximo de três, `cancelled` não.
- Todos os atendimentos duram 30 minutos.
- Datas da API usam RFC 3339.
- A primeira fase não inclui frontend, IA, RAG ou WhatsApp.
- Identificação por telefone destinada ao ambiente local, sem senha ou OTP.
- Logs terão ID de requisição, duração e códigos de erro, sem tokens ou dados pessoais.

Na fase 2, as tools reutilizarão os casos de uso existentes. Mutações prepararão ações; a conversa armazenará seu ID e a confirmação explícita do paciente acionará a execução.

Avaliações usarão cenários manuais em português e relógio controlado. Execuções com modelos registrarão modelo, prompt, entradas, tools e resultados. Reprodução exata usará componentes determinísticos e respostas gravadas.

No RAG, documentos serão conteúdo de consulta; citações serão vinculadas aos trechos recuperados e falta de suporte produzirá abstention.

Go e Docker Compose estão instalados. O Docker Engine precisará estar em execução para os testes de integração.
