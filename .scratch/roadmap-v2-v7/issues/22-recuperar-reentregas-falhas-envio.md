# 22: V7 — Recuperar reentregas e falhas de envio

**What to build:** Reentregas de eventos do WhatsApp e falhas ao enviar respostas são recuperadas sem executar novamente operações de agendamento já concluídas. O estado de entrega permanece inspecionável após reinícios.

**Blocked by:** 20 — Conversar pelo WhatsApp com paciente conhecido.

**Status:** ready-for-agent

- [ ] O identificador estável do evento ou mensagem do provedor é mapeado para a deduplicação da aplicação; reentregas não geram nova execução do agente para a mesma mensagem.
- [ ] Uma resposta produzida é registrada para entrega de modo que um reinício entre processar a mensagem e enviá-la não a descarte silenciosamente.
- [ ] Falhas transitórias de envio usam tentativas limitadas e configuráveis; falhas definitivas ficam registradas para investigação sem recriar a mutação de negócio.
- [ ] Callbacks de status são processados sem serem confundidos com mensagens do paciente e sem retroceder indevidamente um estado de entrega mais recente.
- [ ] Uma resposta entregue ou com entrega incerta não implica garantia de exatamente uma entrega sem suporte do provedor; a política de recuperação dessa incerteza é explícita e testável.
- [ ] Testes com eventos duplicados, falha de envio e reinício demonstram uma única mutação, recuperação da resposta e ausência de retry infinito.
