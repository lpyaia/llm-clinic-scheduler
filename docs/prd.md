# PRD — Assistente Inteligente de Agendamento para Clínica

## 1. Visão do Produto

Construir um assistente inteligente voltado a pacientes de uma clínica médica.

O sistema permitirá que pacientes gerenciem consultas, exames e procedimentos por meio de conversas em linguagem natural.

O projeto tem como principal objetivo servir como ambiente de aprendizado prático de AI Engineering, com foco em:

- agentes baseados em LLM;
- tool calling;
- conversas multi-turn;
- estado de conversação;
- RAG;
- embeddings;
- busca vetorial;
- avaliações de sistemas de IA;
- observabilidade;
- guardrails;
- integração entre componentes probabilísticos e lógica determinística.

A primeira versão não utilizará IA. O projeto será desenvolvido de forma incremental, começando por um backend determinístico de agendamento.

---

# 2. Objetivos

O sistema deverá permitir que pacientes:

- busquem horários disponíveis;
- agendem consultas, exames ou procedimentos;
- visualizem seus agendamentos;
- remarquem um agendamento existente;
- cancelem um agendamento;
- façam perguntas sobre informações operacionais da clínica;
- interajam futuramente por WhatsApp.

A experiência futura deverá permitir conversas naturais, incluindo solicitações vagas como:

> "Quero marcar com um cardiologista na semana que vem à tarde."

ou:

> "Não consigo ir na quarta. Tem algum horário mais para o fim da semana?"

---

# 3. Objetivos de Aprendizado

O projeto deve priorizar oportunidades de aprendizado em AI Engineering em vez de realismo comercial completo.

Os principais conceitos a serem explorados são:

- LLM provider abstraction;
- structured output;
- tool/function calling;
- agent orchestration;
- multi-turn conversations;
- persistent conversation state;
- RAG;
- document ingestion;
- chunking;
- embeddings;
- metadata filtering;
- vector search;
- reranking futuramente;
- citations;
- hallucination prevention;
- agent evaluations;
- RAG evaluations;
- observability;
- tracing;
- token usage;
- latency;
- model cost;
- deterministic authorization and business rules;
- graceful recovery from tool failures.

---

# 4. Usuário Principal

O usuário principal é o paciente da clínica.

Funcionários da clínica não fazem parte do escopo inicial como usuários do sistema.

---

# 5. Escopo da Clínica

O sistema atenderá somente uma clínica.

Suporte a múltiplas clínicas ou redes de clínicas está fora do escopo.

---

# 6. Tipos de Atendimento

O sistema suportará:

- consultas;
- exames;
- procedimentos.

Inicialmente, todo agendamento estará associado a um médico.

Recursos adicionais, como salas, equipamentos, técnicos ou máquinas, não serão modelados na primeira versão.

Uma evolução futura poderá introduzir recursos genéricos de agendamento.

---

# 7. Modelo de Agendamento

Um agendamento deverá conter, no mínimo:

- paciente;
- médico;
- tipo de agendamento;
- data e hora;
- duração;
- status.

A duração padrão será de 30 minutos.

Não haverá campo obrigatório de motivo ou observações.

---

# 8. Status de Agendamento

Os estados serão controlados de forma determinística pela aplicação.

Estados iniciais:

- `scheduled`;
- `cancelled`;
- `completed`.

O LLM nunca deverá decidir diretamente o estado de um agendamento.

---

# 9. Disponibilidade

A disponibilidade dos médicos será representada por slots individuais.

Não serão utilizadas regras recorrentes como:

> "Dr. Silva atende de segunda a sexta, das 9h às 17h."

Em vez disso, serão cadastrados slots específicos disponíveis para cada médico.

Exemplo:

```text
Dr. Silva
2026-10-20 09:00
2026-10-20 09:30
2026-10-20 10:00
```

---

# 10. Busca por Horários

Pacientes poderão procurar disponibilidade de duas maneiras:

1. escolhendo primeiro um médico;
2. escolhendo primeiro uma especialidade.

A especialidade será associada ao médico.

O sistema deverá suportar critérios vagos, como:

- "semana que vem";
- "à tarde";
- "qualquer dia depois de terça";
- "o mais cedo possível".

A interpretação desses critérios será responsabilidade do agente nas versões com IA.

A aplicação de agendamento continuará responsável por determinar quais horários realmente estão disponíveis.

---

# 11. Janela de Busca

A busca inicial por horários deverá considerar até duas semanas.

Caso nenhum horário seja encontrado:

1. ampliar a janela para três semanas;
2. depois quatro semanas;
3. continuar adicionando uma semana por tentativa;
4. limitar a busca a no máximo oito semanas.

Essa política deverá ser implementada de forma determinística no backend.

Ela não deverá existir apenas no prompt do LLM.

---

# 12. Limite de Agendamentos

Um paciente poderá possuir no máximo três agendamentos na mesma semana.

Essa regra deverá ser validada pela aplicação antes da criação de qualquer novo agendamento.

O agente não será responsável por aplicar essa regra.

---

# 13. Criação de Agendamento

Antes de criar um agendamento, o sistema deverá:

1. identificar o paciente;
2. identificar o médico;
3. identificar o slot escolhido;
4. validar limite semanal;
5. verificar novamente a disponibilidade;
6. solicitar confirmação explícita do paciente;
7. tentar reservar o horário.

Exemplo:

> "Encontrei quarta-feira às 15h com o Dr. Silva. Deseja confirmar?"

Somente depois de uma confirmação explícita o agendamento poderá ser criado.

---

# 14. Concorrência

Dois pacientes poderão tentar reservar o mesmo horário ao mesmo tempo.

O banco de dados deverá garantir que apenas um deles obtenha o slot.

A garantia não poderá depender:

- do LLM;
- de uma consulta anterior de disponibilidade;
- apenas de lógica no cliente.

A reserva final deverá possuir garantia transacional ou restrição equivalente no banco.

Em caso de conflito, o sistema deverá retornar um erro determinístico de negócio.

Futuramente, o agente deverá ser capaz de reagir ao erro oferecendo alternativas.

---

# 15. Remarcação

Pacientes poderão remarcar um agendamento existente.

A remarcação poderá alterar somente:

- data;
- horário.

Não será possível alterar diretamente:

- médico;
- tipo do agendamento.

Caso o paciente queira outro médico ou outro tipo de atendimento, isso deverá ser tratado como um novo agendamento.

Toda remarcação deverá exigir confirmação explícita.

---

# 16. Cancelamento

Pacientes poderão cancelar agendamentos existentes.

Todo cancelamento deverá exigir confirmação explícita antes da execução.

---

# 17. Identificação do Paciente

Não haverá sistema tradicional de login com usuário e senha.

O paciente terá:

- ID interno;
- nome completo;
- número de telefone.

O número de telefone será a identidade principal utilizada pelo canal de comunicação.

No futuro, quando houver integração com WhatsApp, o número de origem da mensagem poderá ser utilizado para localizar o paciente.

Nome completo poderá ser utilizado durante cadastro ou vinculação inicial.

---

# 18. Autenticação

Autenticação forte não será exigida na primeira versão.

No futuro, o sistema poderá implementar mecanismos como OTP para ações sensíveis.

A arquitetura deverá permitir a adição futura de um estado como:

```text
identity_verified = true | false
```

Autorização deverá sempre ser implementada no backend.

O LLM nunca poderá decidir se uma pessoa está autorizada a alterar determinado agendamento.

---

# 19. Conversação

A versão com IA deverá suportar conversas multi-turn.

Exemplo:

```text
Paciente:
Quero marcar com o Dr. Silva.

Assistente:
Qual dia seria melhor?

Paciente:
Quarta da semana que vem.

Assistente:
Prefere manhã ou tarde?

Paciente:
Tarde.
```

O sistema deverá ser capaz de preservar informações estabelecidas anteriormente.

Possível estado estruturado:

```text
intent
doctor_id
specialty
desired_date
preferred_period
selected_slot_id
pending_action
```

---

# 20. Estado da Conversação

A arquitetura deverá permitir persistência do estado conversacional.

A primeira implementação poderá utilizar armazenamento em memória por trás de uma abstração.

Exemplo:

```go
type ConversationRepository interface {
    Get(...)
    Save(...)
}
```

Implementações possíveis:

```text
MemoryConversationRepository
PostgresConversationRepository
```

Persistência em PostgreSQL deverá ser considerada uma evolução necessária antes da integração real com WhatsApp.

---

# 21. Ações Pendentes

Quando uma operação depender de confirmação, a aplicação deverá armazenar o estado da operação.

Exemplo:

```text
pending_action = CREATE_APPOINTMENT
selected_slot_id = abc123
```

Caso o usuário responda apenas:

> "Sim."

a aplicação não deverá precisar reconstruir todo o contexto exclusivamente por meio do LLM.

---

# 22. Escopo Médico

O assistente não fornecerá aconselhamento médico.

O assistente não deverá:

- diagnosticar;
- interpretar sintomas;
- indicar tratamentos;
- recomendar medicamentos;
- sugerir especialidades com base em sintomas.

Exemplo:

> "Tenho dor de cabeça todos os dias. Qual especialista devo procurar?"

Resposta esperada:

> orientar o paciente a entrar em contato com a equipe da clínica.

A recomendação médica está explicitamente fora do escopo.

---

# 23. Escalonamento Humano

Quando o assistente:

- não entender a solicitação;
- não conseguir resolver uma situação;
- receber uma pergunta fora do escopo;
- receber uma solicitação que exija decisão clínica;

deverá oferecer contato com a equipe da clínica.

---

# 24. Informações da Clínica

O assistente poderá responder perguntas operacionais, incluindo:

- horário de funcionamento;
- endereço;
- contatos;
- estacionamento;
- acessibilidade;
- convênios;
- perfis de médicos;
- especialidades;
- políticas de cancelamento;
- políticas de remarcação;
- preparação para exames e procedimentos;
- perguntas frequentes.

---

# 25. RAG

Informações textuais da clínica serão disponibilizadas por meio de Retrieval-Augmented Generation.

A base de conhecimento utilizará exclusivamente documentos criados pela própria clínica.

Fontes externas de informação médica não serão utilizadas.

---

# 26. Documentos

O projeto deverá possuir documentos reais ou simulados para aprendizado.

Exemplos:

```text
clinic-faq.pdf
clinic-policies.pdf
exam-preparation.pdf
insurance-guide.pdf
doctor-profiles.pdf
clinic-information.pdf
```

O objetivo é permitir prática com:

- extração de texto;
- chunking;
- embeddings;
- metadata;
- indexação;
- recuperação;
- geração baseada em contexto.

---

# 27. Dados Estruturados vs RAG

Nem toda informação será obtida via RAG.

Informações estruturadas deverão preferencialmente utilizar banco de dados ou ferramentas determinísticas.

Exemplos:

```text
doctor
specialty
appointment availability
patient
appointment
```

Conteúdo textual será um candidato melhor para RAG.

Exemplos:

```text
exam preparation
clinic policies
FAQ
long-form doctor information
general clinic instructions
```

---

# 28. Citações

Respostas derivadas do RAG deverão incluir indicação da fonte utilizada.

Isso será importante principalmente durante o desenvolvimento para validar o comportamento do sistema.

Exemplo:

```text
Fonte:
exam-preparation.pdf — seção "Ultrassom abdominal"
```

---

# 29. Ausência de Informação

Caso o sistema não encontre informação confiável na base RAG, deverá dizer que não consegue responder.

Não deverá inventar ou completar a resposta com conhecimento geral do modelo.

Exemplo:

> "Não encontrei essa informação nos materiais da clínica."

Escalonamento automático para atendente não será obrigatório nesse caso.

---

# 30. Versionamento de Documentos

Versionamento não será implementado inicialmente.

Será assumido que os documentos carregados são sempre as versões mais recentes.

---

# 31. Tool Calling

O agente deverá utilizar structured tool calling.

Ferramentas previstas incluem:

```text
find_available_slots
create_appointment
list_appointments
reschedule_appointment
cancel_appointment
search_clinic_knowledge
```

Ferramentas adicionais poderão surgir conforme o projeto evoluir.

---

# 32. Responsabilidades do LLM

O LLM poderá:

- interpretar linguagem natural;
- identificar intenção;
- interpretar datas relativas;
- interpretar preferências;
- decidir qual ferramenta chamar;
- extrair parâmetros;
- conduzir diálogo;
- pedir esclarecimentos;
- apresentar resultados;
- explicar erros de negócio;
- sintetizar respostas baseadas no RAG.

---

# 33. Responsabilidades Determinísticas

O LLM não poderá ser responsável por:

- disponibilidade real;
- concorrência;
- limite semanal de agendamentos;
- autorização;
- persistência;
- transações;
- status de agendamento;
- criação final do agendamento;
- cancelamento final;
- remarcação final;
- regras de agenda;
- validação de ownership;
- controle de slots.

Regra arquitetural principal:

> O LLM interpreta e orquestra; o backend protege invariantes.

---

# 34. Independência de Provedor de LLM

O sistema deverá ser provider-agnostic.

Tipos específicos de OpenAI, Anthropic ou qualquer outro fornecedor não deverão contaminar o domínio ou a camada de aplicação.

Deverá existir uma abstração baseada nas necessidades do sistema.

Exemplo conceitual:

```go
type LanguageModel interface {
    Generate(ctx context.Context, request AgentRequest) (AgentResponse, error)
}
```

Não será objetivo criar uma abstração universal capaz de representar todos os recursos de todos os provedores.

A interface deverá suportar apenas as capacidades necessárias ao projeto.

---

# 35. Idioma

A interface conversacional será inicialmente disponibilizada somente em português.

Internacionalização está fora do escopo.

---

# 36. Avaliações

O projeto deverá possuir uma suíte de avaliações dedicada ao comportamento do sistema de IA.

Casos deverão incluir:

- seleção correta de ferramenta;
- extração correta de argumentos;
- interpretação de linguagem vaga;
- interpretação de datas relativas;
- preservação de contexto multi-turn;
- solicitação correta de confirmação;
- recusa a aconselhamento médico;
- recuperação após conflito de slot;
- comportamento quando nenhuma disponibilidade é encontrada;
- recuperação correta de documentos;
- precisão das citações;
- ausência de afirmações não suportadas.

---

# 37. Avaliações de RAG

A avaliação da camada de recuperação deverá considerar, futuramente:

- recall\@k;
- relevância dos documentos recuperados;
- groundedness;
- answer correctness;
- citation correctness;
- unsupported claims.

Datasets de avaliação poderão ser criados manualmente.

---

# 38. Observabilidade

O sistema deverá expor informações úteis para investigação do comportamento do agente.

Métricas desejadas:

- latência total;
- latência do modelo;
- número de chamadas ao modelo;
- tokens de entrada;
- tokens de saída;
- custo estimado;
- número de tool calls;
- tool failures;
- erros da aplicação;
- número de passos por execução do agente.

---

# 39. Tracing

Durante desenvolvimento, deverá ser possível visualizar o fluxo completo de uma execução.

Exemplo:

```text
user message
    ↓
model request
    ↓
model decision
    ↓
tool call
    ↓
tool input
    ↓
tool result
    ↓
next model call
    ↓
final response
```

O tracing será considerado requisito importante para aprendizado e debugging.

---

# 40. Interface HTTP

O sistema utilizará APIs REST.

A arquitetura deverá separar claramente:

```text
HTTP layer
Application layer
Domain
Infrastructure
AI/Agent adapters
```

WhatsApp deverá futuramente atuar como mais um adapter de entrada.

---

# 41. Streaming

Streaming de respostas não será implementado inicialmente.

O sistema trabalhará com request/response tradicional.

Streaming poderá ser explorado futuramente, mas não é necessário para a integração planejada com WhatsApp.

---

# 42. Persistência

Banco principal:

```text
PostgreSQL
```

A extensão:

```text
pgvector
```

será utilizada para armazenamento e busca de embeddings.

---

# 43. Stack Principal

Backend:

```text
Go
```

Banco:

```text
PostgreSQL + pgvector
```

Infraestrutura local:

```text
Docker
Docker Compose
```

Não haverá deployment em cloud inicialmente.

---

# 44. Escala

O sistema será tratado como projeto de aprendizado de baixo tráfego.

Não será necessário otimizar para:

- milhares de usuários simultâneos;
- múltiplas regiões;
- alta disponibilidade;
- autoscaling;
- sharding.

Entretanto, invariantes críticas de negócio e concorrência deverão ser implementadas corretamente.

---

# 45. WhatsApp

Integração com WhatsApp é um objetivo futuro.

O domínio e a aplicação não deverão depender de WhatsApp.

Arquitetura esperada:

```text
WhatsApp
    ↓
Webhook adapter
    ↓
Conversation application
    ↓
Agent
    ↓
Tools
    ↓
Scheduling / Knowledge services
```

Antes dessa etapa, poderá ser utilizada uma interface HTTP ou uma interface simples de chat para desenvolvimento.

---

# 46. Não Objetivos

Não fazem parte do escopo inicial:

- aconselhamento médico;
- diagnóstico;
- prescrição;
- triagem clínica;
- recomendação de especialidade baseada em sintomas;
- prontuário eletrônico;
- integração com sistemas reais de hospitais;
- pagamentos;
- faturamento;
- múltiplas clínicas;
- autenticação tradicional;
- recursos médicos complexos;
- deployment em produção;
- alta escala;
- streaming;
- documentação versionada;
- fontes médicas externas.

---

# 47. Roadmap

## V1 — Scheduling Core

Sem LLM.

Implementar backend determinístico contendo:

- pacientes;
- médicos;
- especialidades;
- tipos de agendamento;
- slots individuais;
- consultas/exames/procedimentos;
- busca de disponibilidade;
- janela progressiva de busca;
- criação;
- listagem;
- cancelamento;
- remarcação;
- limite de três agendamentos por semana;
- confirmação representada no fluxo da aplicação;
- prevenção de double-booking;
- PostgreSQL;
- REST API;
- testes unitários;
- testes de integração.

Objetivo:

Construir um sistema confiável que futuramente poderá ser operado por um agente.

---

## V2 — Agent

Adicionar:

- LLM provider abstraction;
- agent loop;
- structured output;
- tool calling;
- conversational intent;
- multi-turn conversations;
- conversation state;
- confirmação via linguagem natural;
- recuperação de erros de tools;
- português como idioma da interface.

Objetivo:

Permitir que o paciente opere o scheduling core por linguagem natural.

---

## V3 — Evaluation & Observability

Adicionar:

- traces;
- token metrics;
- latency metrics;
- model call metrics;
- tool call metrics;
- estimated cost;
- evaluation dataset;
- automated agent evaluations.

Objetivo:

Tornar o comportamento do agente mensurável e depurável.

---

## V4 — RAG

Adicionar:

- documentos da clínica;
- ingestion pipeline;
- chunking;
- embeddings;
- pgvector;
- metadata;
- retrieval;
- `search_clinic_knowledge`;
- respostas grounded;
- citações;
- comportamento de abstention.

Objetivo:

Permitir respostas confiáveis sobre informações da clínica.

---

## V5 — RAG Evaluation

Adicionar:

- retrieval test dataset;
- recall\@k;
- groundedness checks;
- citation correctness;
- experimentos com chunking;
- experimentos com top-k;
- possíveis experimentos com reranking.

Objetivo:

Comparar estratégias de RAG quantitativamente.

---

## V6 — Persistent Conversations

Adicionar:

- persistência de conversas;
- persistência de mensagens;
- persistência de structured conversation state;
- proteção contra processamento concorrente de mensagens.

Objetivo:

Preparar o sistema para canais assíncronos.

---

## V7 — WhatsApp

Adicionar:

- webhook;
- mapeamento por número de telefone;
- onboarding inicial;
- adapter para mensagens;
- envio de respostas.

Objetivo:

Permitir interação por WhatsApp mantendo o domínio independente do canal.

---

# 48. Critérios de Sucesso

O projeto será considerado bem-sucedido se:

1. regras de agendamento puderem funcionar completamente sem LLM;
2. nenhum double-booking for possível;
3. o limite semanal for aplicado deterministicamente;
4. o agente conseguir operar a API via ferramentas;
5. conversas vagas e multi-turn forem suportadas;
6. mutações exigirem confirmação;
7. o agente não fornecer aconselhamento médico;
8. consultas à base RAG produzirem respostas grounded;
9. respostas RAG incluírem fontes;
10. ausência de informação resultar em abstention;
11. comportamento do agente puder ser reproduzido e avaliado;
12. traces permitirem entender decisões e chamadas de ferramentas;
13. provider de LLM puder ser substituído sem alteração do domínio;
14. integração futura com WhatsApp não exigir alteração das regras de negócio.

---

# 49. Princípio Arquitetural Central

A separação entre componentes probabilísticos e determinísticos deverá orientar todas as decisões.

```text
LLM
- entende linguagem
- interpreta intenção
- decide ferramentas
- conduz conversa

Backend
- aplica regras
- protege dados
- controla concorrência
- valida autorização
- executa transações
- mantém invariantes
```

Em resumo:

> Use IA para interpretar e orquestrar. Use código determinístico para garantir correção.
