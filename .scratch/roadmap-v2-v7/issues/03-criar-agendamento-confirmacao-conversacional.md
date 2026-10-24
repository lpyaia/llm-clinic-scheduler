# 03: V2 — Criar agendamento com confirmação conversacional

**What to build:** Paciente escolhe um horário apresentado pelo agente, recebe o resumo da ação pendente e confirma o agendamento em linguagem natural. A aplicação guarda o ID da ação e usa o mecanismo de preparação e confirmação já existente.

**Blocked by:** 02 — Buscar horários em conversa multi-turn.

**Status:** ready-for-agent

- [ ] A escolha identifica um slot retornado e a categoria desejada; informações ausentes são esclarecidas antes da preparação.
- [ ] A ferramenta de criação prepara a ação existente e apresenta seu resumo, validade e pedido explícito de confirmação sem reservar o slot.
- [ ] Uma confirmação explícita posterior ao resumo executa somente a ação pendente associada àquela conversa e ao paciente; o modelo não pode confirmar por iniciativa própria.
- [ ] Negação, resposta ambígua e mudança de horário ou categoria não executam a proposta anterior; uma proposta alterada exige novo resumo e confirmação.
- [ ] Ausência de ação pendente ou ação expirada não permite criar uma reserva; reenvio da confirmação usa a idempotência existente e não duplica o agendamento.
- [ ] Testes do fluxo completo demonstram criação confirmada, recusa, ambiguidade, expiração, troca de proposta e tentativa de confirmação de ação alheia.
