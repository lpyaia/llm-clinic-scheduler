# 05: V2 — Cancelar por conversa

**What to build:** Paciente escolhe um agendamento próprio e solicita seu cancelamento em português. O agente apresenta o resumo e só executa a ação pendente após confirmação explícita.

**Blocked by:** 03 — Criar agendamento com confirmação conversacional.

**Status:** ready-for-agent

- [ ] Solicitações vagas como cancelar minha consulta pedem esclarecimento quando existem vários agendamentos elegíveis.
- [ ] O resumo identifica o atendimento a cancelar antes de pedir confirmação.
- [ ] Somente uma confirmação explícita posterior ao resumo executa cancel_appointment para a ação armazenada; negação ou ambiguidade não cancela.
- [ ] Agendamentos alheios, já iniciados ou em estado não alterável permanecem protegidos pelos casos de uso existentes.
- [ ] Uma confirmação repetida não cancela outro atendimento nem gera uma segunda operação; o resultado apresentado corresponde ao resultado determinístico.
- [ ] Testes verificam cancelamento confirmado, recusa, ambiguidade, ownership, inelegibilidade e repetição de confirmação.
