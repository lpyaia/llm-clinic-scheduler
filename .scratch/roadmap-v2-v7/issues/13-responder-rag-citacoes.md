# 13: V4 — Responder perguntas com RAG e citações

**What to build:** Paciente pergunta sobre uma informação operacional da clínica e o agente consulta search_clinic_knowledge, responde em português com fontes e se abstém quando não encontra suporte confiável.

**Blocked by:** 01 — Consultar agendamentos pelo agente; 12 — Ingerir e recuperar um documento da clínica.

**Status:** ready-for-agent

- [ ] Perguntas operacionais usam a ferramenta de conhecimento; médicos, especialidades, disponibilidade e agendamentos estruturados continuam usando as ferramentas determinísticas correspondentes.
- [ ] A resposta é sustentada pelos trechos recuperados e apresenta documento e localização ou seção disponível na fonte.
- [ ] As citações apontam para trechos efetivamente recuperados; o modelo não pode inventar uma fonte ou identificador de trecho.
- [ ] Quando os materiais não sustentam a resposta, o agente informa que não encontrou a informação e não completa com conhecimento geral do modelo.
- [ ] Falha na recuperação não é apresentada como confirmação de informação nem como resposta factual; aconselhamento médico continua fora do escopo.
- [ ] Testes do fluxo conversacional verificam uma resposta citada, uma pergunta sem suporte, uma falha de recuperação e o roteamento de uma busca de horários.
