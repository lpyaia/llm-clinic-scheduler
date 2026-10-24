# 12: V4 — Ingerir e recuperar um documento da clínica

**What to build:** Um operador carrega um documento simulado de FAQ da própria clínica e consulta os trechos relevantes por similaridade em um fluxo local completo. Esta primeira fatia verifica a recuperação antes da integração conversacional.

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

- [ ] Um documento de FAQ simulado acompanha o fluxo de demonstração, sem dados reais de pacientes ou fontes médicas externas.
- [ ] O carregamento extrai texto, produz chunks e embeddings, armazena conteúdo e vetores em PostgreSQL com pgvector e permite consultar por pergunta.
- [ ] Cada resultado preserva documento, trecho e localização disponível na fonte para permitir citações verificáveis.
- [ ] Consultas relevantes retornam os trechos esperados em ordem de similaridade; documento vazio, falha de extração e falha de embeddings têm resultados claros.
- [ ] Dimensionalidade e configuração de embeddings são validadas; vetores incompatíveis não são comparados silenciosamente.
- [ ] Testes de integração com PostgreSQL/pgvector e embeddings controlados verificam ingestão e busca; um comando ou interface local documentada demonstra o percurso completo.
