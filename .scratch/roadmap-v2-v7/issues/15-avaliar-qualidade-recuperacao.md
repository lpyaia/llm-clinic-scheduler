# 15: V5 — Avaliar qualidade da recuperação

**What to build:** Uma pessoa executa um dataset manual de perguntas e fontes relevantes e mede se a busca recupera os materiais corretos da clínica, inclusive quando não há resposta no acervo.

**Blocked by:** 12 — Ingerir e recuperar um documento da clínica.

**Status:** ready-for-agent

- [ ] O dataset identifica perguntas, documentos ou trechos relevantes e casos sem resposta conhecida, usando um acervo de avaliação controlado.
- [ ] O executor calcula recall@k com definição documentada e apresenta relevância e posições dos resultados por pergunta.
- [ ] Perguntas sem fonte relevante têm tratamento explícito e não causam divisão por zero nem distorcem silenciosamente a métrica.
- [ ] O relatório identifica acervo, embedding, configuração e valor de k usados, permitindo comparar execuções.
- [ ] Resultados agregados permitem abrir os casos de fontes esperadas ausentes e de materiais irrelevantes recuperados.
- [ ] Fixtures de recuperação conhecida demonstram o cálculo de recall@k, múltiplas fontes relevantes e casos sem resposta.
