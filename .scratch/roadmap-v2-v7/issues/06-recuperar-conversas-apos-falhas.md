# 06: V2 — Recuperar conversas após falhas

**What to build:** Quando uma ferramenta ou o modelo falha, o paciente recebe uma explicação em português e um próximo passo coerente com o estado real. A conversa consegue retomar criação, remarcação e cancelamento sem declarar operações que não ocorreram.

**Blocked by:** 04 — Remarcar por conversa; 05 — Cancelar por conversa.

**Status:** ready-for-agent

- [ ] SLOT_UNAVAILABLE oferece nova busca; WEEKLY_LIMIT_REACHED explica a restrição e oferece um caminho permitido, sem contornar a regra.
- [ ] ACTION_EXPIRED e APPOINTMENT_CHANGED descartam a proposta obsoleta e exigem nova preparação e confirmação antes de qualquer nova tentativa de mutação.
- [ ] Quando não há disponibilidade, a resposta informa o resultado da janela pesquisada e pede mudança de critérios em vez de inventar horários.
- [ ] Falhas transitórias respeitam o rollback e a idempotência existentes; um resultado incerto é consultado antes de repetir a mesma confirmação.
- [ ] Falhas de modelo, timeout e esgotamento de passos encerram a execução de forma controlada e nunca afirmam sucesso sem resultado da ferramenta.
- [ ] Testes com falhas injetadas demonstram recuperação, nova confirmação para propostas alteradas e preservação do agendamento original em remarcações malsucedidas.
