# 14: V4 — Consultar o acervo operacional com filtros

**What to build:** Paciente consulta políticas, preparação de exames, convênios, perfis e informações gerais da clínica. O acervo inclui documentos simulados representativos, metadados úteis e reingestão da versão atual.

**Blocked by:** 13 — Responder perguntas com RAG e citações.

**Status:** ready-for-agent

- [ ] O acervo cobre FAQ, políticas, preparação, convênios, perfis de médicos e informações gerais, incluindo documentos PDF simulados com localização da fonte preservada.
- [ ] Metadados permitem restringir a recuperação ao assunto e, quando aplicável, ao exame, procedimento ou médico mencionado na pergunta.
- [ ] Uma pergunta com entidade ambígua pede esclarecimento em vez de responder com preparação ou política de outro contexto.
- [ ] Reingerir um documento substitui seus trechos e vetores atuais sem duplicação nem manutenção de histórico; trechos removidos deixam de aparecer nas consultas.
- [ ] Conteúdo do documento é tratado como dado: instruções nele contidas não alteram as regras do agente, não autorizam mutações e não ocultam citações.
- [ ] Testes verificam filtros, localização das fontes, atualização sem resultados obsoletos e documento contendo instruções indevidas.
