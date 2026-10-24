# Roadmap V2–V7

Recorte aprovado do PRD: 22 tickets a partir da V2. A V1 — Scheduling Core já foi implementada e não faz parte deste backlog.

Os tickets foram publicados como GitHub Issues com a label `ready-for-agent`. Foram verificadas 27 dependências nativas entre as issues. Os arquivos em `issues/` preservam a numeração e os critérios de aceite aprovados; a fonte de acompanhamento é o GitHub.

## Project

Destino configurado: [Project 1 de lpyaia](https://github.com/users/lpyaia/projects/1).

As 22 issues foram vinculadas ao Project pelo navegador autenticado e aparecem na coluna `Backlog`. A associação foi conferida após recarregar o quadro, verificando a contagem e os identificadores dos tickets. As dependências nativas continuam visíveis nos cartões bloqueados.

O campo `project_added` está confirmado para todos os tickets no manifesto. A importação usou o status `Backlog` do próprio Project; nenhuma coluna ou opção de status foi criada ou renomeada.

## Tickets

| Ticket | Fase | Issue | Bloqueado por |
| --- | --- | --- | --- |
| 01 | V2 | [#7 — Consultar agendamentos pelo agente](https://github.com/lpyaia/llm-clinic-scheduler/issues/7) | Nenhum |
| 02 | V2 | [#10 — Buscar horários em conversa multi-turn](https://github.com/lpyaia/llm-clinic-scheduler/issues/10) | [#7](https://github.com/lpyaia/llm-clinic-scheduler/issues/7) |
| 03 | V2 | [#11 — Criar agendamento com confirmação conversacional](https://github.com/lpyaia/llm-clinic-scheduler/issues/11) | [#10](https://github.com/lpyaia/llm-clinic-scheduler/issues/10) |
| 04 | V2 | [#12 — Remarcar por conversa](https://github.com/lpyaia/llm-clinic-scheduler/issues/12) | [#11](https://github.com/lpyaia/llm-clinic-scheduler/issues/11) |
| 05 | V2 | [#13 — Cancelar por conversa](https://github.com/lpyaia/llm-clinic-scheduler/issues/13) | [#11](https://github.com/lpyaia/llm-clinic-scheduler/issues/11) |
| 06 | V2 | [#14 — Recuperar conversas após falhas](https://github.com/lpyaia/llm-clinic-scheduler/issues/14) | [#12](https://github.com/lpyaia/llm-clinic-scheduler/issues/12), [#13](https://github.com/lpyaia/llm-clinic-scheduler/issues/13) |
| 07 | V2 | [#15 — Recusar aconselhamento médico e oferecer contato humano](https://github.com/lpyaia/llm-clinic-scheduler/issues/15) | [#7](https://github.com/lpyaia/llm-clinic-scheduler/issues/7) |
| 08 | V3 | [#16 — Investigar uma execução por trace](https://github.com/lpyaia/llm-clinic-scheduler/issues/16) | [#7](https://github.com/lpyaia/llm-clinic-scheduler/issues/7) |
| 09 | V3 | [#17 — Medir latência, consumo e custo](https://github.com/lpyaia/llm-clinic-scheduler/issues/17) | [#16](https://github.com/lpyaia/llm-clinic-scheduler/issues/16) |
| 10 | V3 | [#18 — Executar avaliações reproduzíveis do agente](https://github.com/lpyaia/llm-clinic-scheduler/issues/18) | [#7](https://github.com/lpyaia/llm-clinic-scheduler/issues/7) |
| 11 | V3 | [#19 — Cobrir os comportamentos da V2 com avaliações](https://github.com/lpyaia/llm-clinic-scheduler/issues/19) | [#14](https://github.com/lpyaia/llm-clinic-scheduler/issues/14), [#15](https://github.com/lpyaia/llm-clinic-scheduler/issues/15), [#18](https://github.com/lpyaia/llm-clinic-scheduler/issues/18) |
| 12 | V4 | [#20 — Ingerir e recuperar um documento da clínica](https://github.com/lpyaia/llm-clinic-scheduler/issues/20) | Nenhum |
| 13 | V4 | [#21 — Responder perguntas com RAG e citações](https://github.com/lpyaia/llm-clinic-scheduler/issues/21) | [#7](https://github.com/lpyaia/llm-clinic-scheduler/issues/7), [#20](https://github.com/lpyaia/llm-clinic-scheduler/issues/20) |
| 14 | V4 | [#22 — Consultar o acervo operacional com filtros](https://github.com/lpyaia/llm-clinic-scheduler/issues/22) | [#21](https://github.com/lpyaia/llm-clinic-scheduler/issues/21) |
| 15 | V5 | [#23 — Avaliar qualidade da recuperação](https://github.com/lpyaia/llm-clinic-scheduler/issues/23) | [#20](https://github.com/lpyaia/llm-clinic-scheduler/issues/20) |
| 16 | V5 | [#24 — Avaliar fundamentação, respostas e citações](https://github.com/lpyaia/llm-clinic-scheduler/issues/24) | [#18](https://github.com/lpyaia/llm-clinic-scheduler/issues/18), [#21](https://github.com/lpyaia/llm-clinic-scheduler/issues/21) |
| 17 | V5 | [#25 — Comparar estratégias de RAG](https://github.com/lpyaia/llm-clinic-scheduler/issues/25) | [#22](https://github.com/lpyaia/llm-clinic-scheduler/issues/22), [#23](https://github.com/lpyaia/llm-clinic-scheduler/issues/23), [#24](https://github.com/lpyaia/llm-clinic-scheduler/issues/24) |
| 18 | V6 | [#26 — Retomar conversas após reiniciar a aplicação](https://github.com/lpyaia/llm-clinic-scheduler/issues/26) | [#11](https://github.com/lpyaia/llm-clinic-scheduler/issues/11) |
| 19 | V6 | [#27 — Processar mensagens concorrentes sem perder estado](https://github.com/lpyaia/llm-clinic-scheduler/issues/27) | [#26](https://github.com/lpyaia/llm-clinic-scheduler/issues/26) |
| 20 | V7 | [#28 — Conversar pelo WhatsApp com paciente conhecido](https://github.com/lpyaia/llm-clinic-scheduler/issues/28) | [#27](https://github.com/lpyaia/llm-clinic-scheduler/issues/27) |
| 21 | V7 | [#29 — Cadastrar paciente no primeiro contato](https://github.com/lpyaia/llm-clinic-scheduler/issues/29) | [#28](https://github.com/lpyaia/llm-clinic-scheduler/issues/28) |
| 22 | V7 | [#30 — Recuperar reentregas e falhas de envio](https://github.com/lpyaia/llm-clinic-scheduler/issues/30) | [#28](https://github.com/lpyaia/llm-clinic-scheduler/issues/28) |

## Próximos tickets disponíveis

- [#7 — V2: Consultar agendamentos pelo agente](https://github.com/lpyaia/llm-clinic-scheduler/issues/7).
- [#20 — V4: Ingerir e recuperar um documento da clínica](https://github.com/lpyaia/llm-clinic-scheduler/issues/20), que é tecnicamente independente da V2. Para seguir a ordem do roadmap, iniciar por #7.

Uma label `ready-for-agent` não elimina os bloqueios: um ticket só pode começar quando todos os seus bloqueadores estiverem concluídos.

## Verificação da publicação

- 22 issues canônicas abertas, todas com os critérios de aceite aprovados e a label `ready-for-agent`.
- 27 arestas de bloqueio nativas conferidas pela API do GitHub.
- 22 issues vinculadas ao Project 1 e conferidas no backlog pelo navegador autenticado.
- Labels padrão configuradas: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human` e `wontfix`.
- Duas cópias geradas durante a tentativa inicial de publicação foram fechadas como duplicatas: #8 de #10, e #9 de #7.
- Nenhuma issue da V1 ou issue pai foi criada ou modificada.

O manifesto `publication.json` relaciona a numeração local aos identificadores reais do GitHub, aos bloqueios e à situação de associação ao Project.
