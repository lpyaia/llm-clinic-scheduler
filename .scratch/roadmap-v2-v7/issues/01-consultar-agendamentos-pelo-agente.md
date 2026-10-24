# 01: V2 — Consultar agendamentos pelo agente

**What to build:** Paciente autenticado pergunta em português quais atendimentos tem marcados e recebe seus agendamentos pela interface HTTP conversacional. O agente opera o Scheduling Core existente por uma ferramenta estruturada, usando uma abstração de LLM e um adapter funcional de fornecedor.

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

- [ ] A entrada HTTP resolve o paciente pela sessão existente; parâmetros do modelo não permitem escolher outra identidade ou consultar agendamentos alheios.
- [ ] Uma pergunta em português aciona list_appointments e produz uma resposta fiel aos resultados, inclusive quando a lista está vazia.
- [ ] A abstração do modelo suporta mensagens, respostas estruturadas, tool calls e resultados sem expor tipos do fornecedor ao domínio ou aos casos de uso.
- [ ] O adapter de um fornecedor pode ser substituído por um modelo simulado sem mudar as regras de agendamento.
- [ ] Argumentos inválidos, ferramentas desconhecidas e respostas malformadas não executam operações; limites de passos e cancelamento impedem loops sem fim.
- [ ] Testes HTTP e do fluxo com modelo simulado verificam sucesso, isolamento entre pacientes, falha do modelo e encerramento por limite; um roteiro documenta a execução local com o adapter real.
