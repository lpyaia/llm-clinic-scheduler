# 04: V2 — Remarcar por conversa

**What to build:** Paciente identifica um de seus agendamentos, escolhe outro horário e confirma a remarcação em português. O agente reutiliza o fluxo de ações pendentes e mantém as restrições do Scheduling Core.

**Blocked by:** 03 — Criar agendamento com confirmação conversacional.

**Status:** ready-for-agent

- [ ] A conversa lista ou identifica agendamentos próprios e pede esclarecimento quando mais de um corresponde ao pedido.
- [ ] A busca de destino mantém o médico do agendamento e apresenta horários reais.
- [ ] O resumo mostra a mudança proposta; somente uma confirmação explícita executa reschedule_appointment pela ação pendente armazenada.
- [ ] A remarcação preserva ID, médico e categoria; pedidos para trocar médico ou tipo são tratados como intenção de novo agendamento, sem cancelar automaticamente o atual.
- [ ] Conflito de destino ou falha da operação preserva o agendamento original e não produz mensagem de sucesso.
- [ ] Testes cobrem identificação ambígua, confirmação, recusa, tentativa de mudança não permitida e falha com preservação da reserva original.
