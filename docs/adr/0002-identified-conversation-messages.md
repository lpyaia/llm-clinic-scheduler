# Mensagens identificadas e revisão da proposta (#27)

HTTP usa `Agent.ReplyMessage` com `message_id` obrigatório e `conversation_revision` da resposta recebida.
O cliente mantém ID e texto nos retries. O PostgreSQL serializa turnos por lock de sessão; a aquisição define a ordem,
sem prometer FIFO de chegada HTTP. A aceitação ocorre no primeiro checkpoint `processing`, após adquirir acesso.
Cancelar durante espera não aceita a mensagem. Os métodos diretos antigos preservam rejeição imediata para seus
consumidores e avaliações; adapters de canal devem usar a entrada identificada.

A revisão enviada pelo cliente vincula a mensagem ao resumo recebido. Ler a revisão no servidor após esperar pelo
pool ou pelo lock poderia vincular um "sim" a uma proposta criada durante a espera. Uma revisão diferente conserva
a possibilidade de buscar ou preparar outra proposta, mas não habilita `respond_to_proposal` nem revoga uma proposta
mais recente por iniciativa própria. O modelo continua interpretando o consentimento; paciente, ação e regras
permanecem sob controle do backend. Omitir revisão usa zero e não autoriza uma proposta com revisão posterior.

O registro por ID fica no estado JSONB existente, com texto, status e resultado ou erro seguro, sob o mesmo commit
do contexto. Isso evita outro protocolo de transação entre mensagens e conversa. Repetir um ID terminal devolve
seu resultado original, mesmo se o contexto mudou depois; outro texto com esse ID é inválido. O escopo é paciente e
conversa. Na primeira mensagem, o ID da conversa deriva dos UUIDs do paciente e da mensagem para recuperar retries
que não receberam `conversation_id`; conhecer esses IDs não remove a validação de ownership.

Um registro `processing` encontrado após adquirir acesso pertence a um turno interrompido. Se houver referência de
confirmação incerta, consulta-se a ação existente, sem repetir a mutação. Sem essa referência, registra-se
`MESSAGE_INTERRUPTED`, sem reexecutar modelo ou tools. Um pedido novo recebe outro ID e é processado após reconciliar
o anterior. Falhas de consulta ou de gravação preservam o checkpoint para outra tentativa. A referência de consulta
anterior à mutação e a revogação anterior à interpretação continuam sendo commits independentes.

Não há worker, fila em memória ou lease dependente de relógio: encerrar a sessão PostgreSQL libera acesso; deadlines
cancelam espera. Quem perdeu a resposta pode reenviar o mesmo ID em qualquer instância. O histórico contém trocas
bem-sucedidas e os registros de mensagens conservam também entradas aceitas que falharam. O formato JSONB permanece
compatível com conversas anteriores sem registros. Atualize todas as instâncias antes de usar a entrada identificada:
uma versão antiga desconhece os registros de deduplicação e poderia descartá-los em uma gravação posterior.
