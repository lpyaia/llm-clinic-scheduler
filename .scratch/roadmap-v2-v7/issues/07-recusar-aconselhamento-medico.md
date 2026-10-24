# 07: V2 — Recusar aconselhamento médico e oferecer contato humano

**What to build:** Paciente que pede diagnóstico, interpretação de sintomas, tratamento, medicamentos ou indicação de especialidade por sintomas recebe uma recusa adequada em português e orientação para procurar a equipe da clínica. Pedidos não compreendidos ou fora do escopo também oferecem esse contato.

**Blocked by:** 01 — Consultar agendamentos pelo agente.

**Status:** ready-for-agent

- [ ] Os cenários de aconselhamento médico previstos no PRD não produzem diagnóstico, recomendação de tratamento, medicamento ou especialidade baseada em sintomas.
- [ ] A recusa é clara, respeitosa e apresenta o canal de contato da clínica obtido de configuração confiável, sem inventar contatos.
- [ ] Solicitações não compreendidas ou que dependem de decisão clínica oferecem contato humano; não é necessário criar um sistema de atendimento humano.
- [ ] Busca por uma especialidade explicitamente escolhida pelo paciente e operações administrativas válidas continuam funcionando.
- [ ] Tentativas de instruir o agente a ignorar essas restrições não habilitam ferramentas ou operações fora do escopo.
- [ ] Testes com modelo simulado e cenários em português cobrem recusas e pedidos administrativos legítimos; os mesmos cenários ficam disponíveis para avaliações posteriores com modelo real.
