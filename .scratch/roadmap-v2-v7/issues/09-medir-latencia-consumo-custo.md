# 09: V3 — Medir latência, consumo e custo

**What to build:** A partir de uma execução instrumentada, é possível observar o esforço e o tempo gastos pelo agente e estimar o custo das chamadas ao modelo sem depender de preços fixos no código.

**Blocked by:** 08 — Investigar uma execução por trace.

**Status:** ready-for-agent

- [ ] Expõe latência total e do modelo, chamadas ao modelo, tokens de entrada e saída, tool calls, falhas de tools, erros da aplicação e passos por execução.
- [ ] Os valores podem ser correlacionados ao trace, incluindo execuções com erro e com múltiplas chamadas ao modelo.
- [ ] O custo estimado usa o consumo informado e uma tabela configurável identificada por fornecedor e modelo; preço ausente não vira custo zero.
- [ ] Consumo não informado pelo fornecedor é marcado como indisponível; chamadas malsucedidas não são omitidas da contagem.
- [ ] Métricas não incluem dados pessoais ou credenciais como labels e podem ser inspecionadas em uma execução local documentada.
- [ ] Testes com consumo e durações controlados verificam agregação, estimativa de custo, erro parcial e dados de consumo indisponíveis.
