# 08: V3 — Investigar uma execução por trace

**What to build:** Durante desenvolvimento, uma pessoa consegue investigar o percurso completo de uma mensagem do paciente até a resposta final e localizar o passo em que o modelo ou uma ferramenta falhou.

**Blocked by:** 01 — Consultar agendamentos pelo agente.

**Status:** ready-for-agent

- [ ] Uma execução correlaciona entrada, solicitações ao modelo, respostas estruturadas, tool calls, argumentos, resultados e resposta final.
- [ ] Cada passo informa identificação, ordem, duração e resultado, incluindo erros e encerramento por limite.
- [ ] Um roteiro local permite consultar uma execução completa e navegar da requisição ao passo que originou uma falha.
- [ ] A captura de conteúdo conversacional usa dados simulados ou redação adequada; tokens de autenticação e segredos não aparecem em traces, nem em logs.
- [ ] Instrumentação é independente do fornecedor de LLM e não altera os resultados dos casos de uso.
- [ ] Testes verificam correlação e ordem em um fluxo com tools, um fluxo com falha e a ausência de credenciais na saída.
