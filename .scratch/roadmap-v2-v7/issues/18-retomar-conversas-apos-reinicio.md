# 18: V6 — Retomar conversas após reiniciar a aplicação

**What to build:** Paciente retoma uma conversa após reiniciar a aplicação sem perder mensagens, preferências ou a referência à proposta aguardando confirmação. PostgreSQL passa a implementar a abstração de armazenamento conversacional.

**Blocked by:** 03 — Criar agendamento com confirmação conversacional.

**Status:** ready-for-agent

- [ ] Conversas, mensagens e estado estruturado são persistidos por migrations explícitas, mantendo uma implementação em memória utilizável em testes.
- [ ] Após reinício, a aplicação recupera histórico, intenção, preferências, seleção e referência à ação pendente para a conversa correta.
- [ ] Uma resposta Sim após reinício confirma somente a ação apresentada anteriormente e ainda válida; ação expirada ou obsoleta exige nova preparação.
- [ ] Ownership é validado pelo backend em leitura e escrita; conhecer o ID de uma conversa alheia não concede acesso.
- [ ] Falha de persistência não gera uma resposta que sugira que o estado foi salvo; o resultado de mutações é reconciliável com as ações pendentes já persistidas.
- [ ] Testes de integração demonstram retomada após recriar o serviço, confirmação válida, expiração e isolamento entre pacientes.
