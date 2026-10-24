# 17: V5 — Comparar estratégias de RAG

**What to build:** Uma pessoa compara estratégias de chunking e top-k usando o mesmo acervo, perguntas e critérios, e obtém um relatório quantitativo para escolher a configuração do RAG.

**Blocked by:** 14 — Consultar o acervo operacional com filtros; 15 — Avaliar qualidade da recuperação; 16 — Avaliar fundamentação, respostas e citações.

**Status:** ready-for-agent

- [ ] Uma matriz configurável executa pelo menos duas estratégias de chunking e dois valores de top-k sobre um snapshot controlado do acervo e do dataset.
- [ ] O experimento registra parâmetros, configuração de embeddings e geração, resultados por cenário e métricas agregadas de recuperação e resposta.
- [ ] Mudanças de chunking reconstroem o índice correspondente e não misturam resultados de configurações diferentes.
- [ ] O relatório permite identificar ganhos e perdas em recall, fundamentação e citações, sem trocar perguntas entre configurações.
- [ ] Reranking é opcional: se experimentado, sua configuração e comparação com a busca sem reranking ficam identificadas; sua implementação não é requisito de conclusão.
- [ ] Um roteiro reproduz uma comparação pequena com resultados verificáveis e registra a conclusão sustentada pelos dados.
