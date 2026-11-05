# Respostas contextuais a propostas (#39)

## Decisão

Adotar `respond_to_proposal` no loop existente, com `confirm`, `reject` e `clarify`.
Alterações usam `create_appointment` ou `find_available_slots`, exigindo outro resumo e mensagem posterior.

| Critério | Tool contextual (escolhida) | Resposta estruturada dedicada |
| --- | --- | --- |
| Chamadas | Uma decisão ou alteração por chamada | Classificação; possível chamada extra para alterações |
| Loop | Mesmo protocolo, correlação e limite de passos | Novo caminho de classificação antes do loop |
| Disponibilidade | Tool somente com vínculo válido | Classificador restrito, seguido de seleção de tools |
| Validação | Schema e decoder compartilhado; sem IDs | Schema de classificação e validação da transição |

O ganho é conservar uma sequência única de coordenação e evitar uma classificação adicional para alterações.
O modelo interpreta somente a mensagem atual em contexto; histórico e dados de ferramentas não autorizam confirmação.
A aplicação escolhe paciente e ação e o núcleo revalida ownership, validade, disponibilidade e idempotência.

## Concorrência e falhas

Toda mensagem usa acesso exclusivo imediato, antes de chamar o modelo, sem reconhecimento textual de confirmação.
Uma conversa ocupada retorna `INVALID_INPUT` (400); conversas distintas continuam independentes.
Antes, apenas confirmações reconhecidas por lista eram rejeitadas e as demais mensagens esperavam na fila.
Agora o cliente deve aguardar a resposta e reenviar qualquer mensagem concorrente.

Sob o acesso exclusivo, o turno captura ação, paciente e revisão, remove a autoridade persistida e grava a revogação.
Somente esse vínculo local pode confirmar; nenhuma preparação ou continuação do loop cria autoridade no mesmo turno.
Um desvio para outra tool invalida o vínculo e remove a tool de resposta das próximas chamadas ao modelo.
Cada nova proposta e cada turno de consumo avançam a revisão; decisões de revisão anterior são rejeitadas.

Rejeição, ambiguidade, mudança de intenção, falha do modelo, resposta inválida e cancelamento após a revogação
exigem nova preparação e resumo antes de confirmar. Antes, uma confirmação reconhecida preservava autoridade
após falha; agora falhas de interpretação também a consomem. Entrada inválida e mensagem rejeitada por ocupação
não iniciam um turno e não revogam a proposta em processamento.

Reenvios bem-sucedidos usam somente a referência à ação concluída, inclusive após sua validade original.
Revogar essa referência nunca cancela o agendamento. Se o núcleo concluir e a gravação final falhar,
o agendamento permanece; consulte a listagem para verificar o resultado. A recuperação ampliada pertence a #14.

Os testes determinísticos verificam execução e efeitos pela interface pública com modelo simulado.
A qualidade da interpretação em português com o fornecedor real é avaliada separadamente em #40.
