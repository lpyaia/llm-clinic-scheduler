# 02: V2 — Buscar horários em conversa multi-turn

**What to build:** Paciente procura horários por nome de médico ou especialidade e refina a busca ao longo de mensagens em português. A conversa preserva intenção e preferências em memória, atrás de uma abstração que permita persistência futura.

**Blocked by:** 01 — Consultar agendamentos pelo agente.

**Status:** ready-for-agent

- [ ] Médicos e especialidades são resolvidos a partir do catálogo existente; nomes ambíguos e informações ausentes geram esclarecimento, sem inventar identificadores.
- [ ] Expressões como semana que vem, à tarde, depois de terça e o mais cedo possível são convertidas em argumentos de find_available_slots usando o relógio injetado e o fuso da clínica.
- [ ] Uma conversa dividida em várias mensagens preserva médico, especialidade, data e período; alterações explícitas do paciente substituem a preferência anterior.
- [ ] A busca reutiliza a política determinística existente de ampliação de janela, respeita intervalos explícitos e apresenta somente slots realmente retornados.
- [ ] O estado estruturado e o histórico pertencem à conversa e ao paciente autenticado; uma conversa não pode acessar o estado de outra.
- [ ] Testes com relógio controlado cobrem datas relativas, ambiguidades, mudança de preferência, ausência de disponibilidade e isolamento de conversas.
