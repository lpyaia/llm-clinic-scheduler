# 16: V5 — Avaliar fundamentação, respostas e citações

**What to build:** A avaliação de respostas RAG permite verificar se cada resposta está correta, se suas afirmações são sustentadas e se as citações correspondem às evidências recuperadas.

**Blocked by:** 10 — Executar avaliações reproduzíveis do agente; 13 — Responder perguntas com RAG e citações.

**Status:** ready-for-agent

- [ ] Cenários de avaliação incluem pergunta, resposta de referência ou rubrica, evidências esperadas e indicação de quando a abstention é correta.
- [ ] O relatório distingue groundedness, correção da resposta, correção das citações, afirmações sem suporte e acerto da abstention.
- [ ] Citações inexistentes, fontes não recuperadas e fontes que não sustentam a afirmação são identificadas como falhas distintas.
- [ ] Verificações determinísticas são usadas quando possíveis; eventuais julgamentos por modelo registram a rubrica, o modelo e suas limitações e ficam inspecionáveis.
- [ ] Há exemplos negativos explícitos de resposta plausível sem evidência, citação errada e abstention indevida.
- [ ] Uma execução com fixtures conhecidas comprova a classificação dos exemplos positivos e negativos e a geração de resultados por cenário.
