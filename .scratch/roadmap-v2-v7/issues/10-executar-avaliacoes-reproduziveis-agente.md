# 10: V3 — Executar avaliações reproduzíveis do agente

**What to build:** Uma pessoa executa cenários de conversa em português e obtém um relatório de expectativas e resultados. O mesmo fluxo permite reprodução determinística e avaliação explícita com um modelo real.

**Blocked by:** 01 — Consultar agendamentos pelo agente.

**Status:** ready-for-agent

- [ ] Um dataset descreve mensagens, condições iniciais, ferramentas e argumentos esperados, efeitos permitidos e resposta esperada em termos de comportamento.
- [ ] O executor controla relógio e dados simulados e aceita modelo simulado ou respostas gravadas para reprodução determinística.
- [ ] Uma opção explícita executa o adapter real; o relatório identifica fornecedor, modelo, prompt, configuração e cenário, sem prometer reprodução exata de geração probabilística.
- [ ] Relatórios apresentam resultado por cenário e totais; falhas de execução são distinguíveis de falhas de comportamento.
- [ ] Expectativas detectam efeitos proibidos e uso incorreto de tools, sem exigir correspondência literal de toda a resposta em linguagem natural.
- [ ] Testes demonstram um cenário aprovado, um reprovado e a repetição determinística com as mesmas entradas.
