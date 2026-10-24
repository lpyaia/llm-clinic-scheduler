# 11: V3 — Cobrir os comportamentos da V2 com avaliações

**What to build:** A suíte de avaliações mede se o agente opera corretamente os fluxos conversacionais da V2 e identifica regressões com resultados inspecionáveis por cenário.

**Blocked by:** 06 — Recuperar conversas após falhas; 07 — Recusar aconselhamento médico e oferecer contato humano; 10 — Executar avaliações reproduzíveis do agente.

**Status:** ready-for-agent

- [ ] O dataset manual em português cobre escolha de ferramenta, extração de argumentos, linguagem vaga, datas relativas, contexto multi-turn e mudança de preferência.
- [ ] Criação, remarcação e cancelamento são avaliados com confirmação positiva, recusa e resposta ambígua; operações sem confirmação contam como falha.
- [ ] Cobre conflito de slot, ausência de disponibilidade, limite semanal, ação expirada, estado alterado e falha transitória.
- [ ] Inclui recusas a aconselhamento médico, contatos humanos e isolamento entre pacientes.
- [ ] Cada cenário possui condições iniciais, relógio, expectativas objetivas e critério de aprovação; o relatório distingue falha operacional de comportamento incorreto.
- [ ] Uma execução determinística documentada cobre toda a suíte, e a execução opcional com modelo real permite inspecionar erros sem modificar dados da aplicação.
