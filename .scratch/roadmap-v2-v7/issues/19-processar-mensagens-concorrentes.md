# 19: V6 — Processar mensagens concorrentes sem perder estado

**What to build:** Mensagens simultâneas da mesma conversa são processadas sem sobrescrever contexto ou repetir a mesma mensagem identificada. O comportamento permanece correto com mais de uma instância da aplicação.

**Blocked by:** 18 — Retomar conversas após reiniciar a aplicação.

**Status:** ready-for-agent

- [ ] A aplicação serializa processamento e atualização de estado por conversa usando coordenação efetiva no PostgreSQL, sem depender apenas de mutex local.
- [ ] Cada mensagem aceita possui identificação estável e estado de processamento; repetir a mesma identificação não reexecuta tools de mutação.
- [ ] Duas mensagens distintas da mesma conversa são tratadas numa ordem definida, sem perder mensagens, confirmar uma proposta substituída ou sobrescrever estado mais recente.
- [ ] Conversas diferentes continuam podendo progredir independentemente.
- [ ] Falha ou reinício durante o processamento permite recuperação sem manter a conversa bloqueada indefinidamente; uma mutação com resultado incerto consulta sua ação existente antes de repetição.
- [ ] Testes concorrentes com duas instâncias e PostgreSQL real verificam ausência de perda de estado, deduplicação e recuperação após interrupção.
