# Ingestão e recuperação de documentos da clínica

O operador importa materiais operacionais e pesquisa trechos classificados por similaridade de cosseno.
Metadados permitem filtrar assunto, exame, procedimento e médico antes do ranking. As fontes preservam documento,
texto, linhas e, para PDFs, página. Sem filtros, o comando pesquisa todo o acervo.

A integração conversacional de #21 usa esse mesmo acervo por `search_clinic_knowledge`, com respostas citadas
e abstention. O comando `knowledge search` continua disponível para inspecionar a recuperação isoladamente.

## Executar

Use Go 1.27+, Docker Desktop e a `.env` descrita no [README principal](../../README.md). O Compose utiliza
`pgvector/pgvector:0.8.7-pg17`, compatível com os dados PostgreSQL 17 do volume existente. Recrie o serviço para
carregar a extensão instalada na nova imagem; preserve o volume:

```powershell
docker compose up -d --wait db
go run ./cmd/clinic migrate
```

A migration instala a extensão `vector` no schema `public` e cria as tabelas de documentos e chunks. O usuário de
migration precisa de permissão para instalar extensões. PostgreSQL 17 sem pgvector instalado não pode aplicá-la.
O rollback remove somente as tabelas desta entrega, preservando a extensão compartilhada.

Configure na `.env` (ou no ambiente) as credenciais e o modelo de embeddings. São independentes de `OPENAI_MODEL`:

```dotenv
OPENAI_API_KEY=<sua-chave>
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_DIMENSIONS=1536
```

O adapter aceita `text-embedding-3-small` (até 1536 dimensões) ou `text-embedding-3-large` (até 3072).
A API recebe explicitamente modelo, dimensionalidade e `encoding_format=float`.
Veja o [contrato de embeddings](https://developers.openai.com/api/reference/resources/embeddings/methods/create)
e os [operadores do pgvector](https://github.com/pgvector/pgvector#querying).

O fluxo abaixo envia o FAQ fictício e a pergunta à OpenAI e pode gerar cobrança. Não exige modelo conversacional.
Execute na raiz do projeto:

```powershell
$imported = go run ./cmd/knowledge ingest -file docs/knowledge/faq-simulated.md | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { throw 'Falha na ingestão' }
$imported

go run ./cmd/knowledge search -question 'Posso estacionar na clínica?' -limit 3
go run ./cmd/knowledge search -question 'A recepção abre no sábado?' -limit 3
```

Cada resultado JSON contém `document_id`, `source`, `chunk` (ordinal a partir de 1), `text`, `start_line`,
`end_line` e `similarity`. Similaridade maior aparece primeiro; empates seguem o UUID do documento e o ordinal do chunk.
O limite é global: os resultados podem vir de vários documentos.
Na primeira pergunta, confira o trecho “Há estacionamento?”; na segunda, “Qual é o horário de atendimento?”.
Os valores de similaridade dependem do modelo; não são probabilidades nem uma garantia de relevância.
O comando apresenta trechos recuperados, sem compor uma resposta clínica ou aplicar limiar de abstention.

O `id` retornado identifica a versão atual nas citações; ele não é um argumento da busca. Os documentos continuam
pesquisáveis após reiniciar o processo. Reimportar o mesmo `source` substitui documento, metadados, chunks e vetores
em uma transação, com outro UUID e sem histórico. Trechos removidos deixam de aparecer em novas recuperações.
A identidade da fonte é o caminho passado em `-file`, comparado exatamente; use sempre a mesma grafia e caminho
relativo à raiz. Arquivos com nomes diferentes são fontes diferentes. A substituição também elimina duplicatas
legadas daquela fonte; fontes antigas não reimportadas permanecem intactas.

## Acervo operacional e filtros (#22)

[corpus.json](corpus.json) lista os materiais simulados e seus metadados, incluindo dois PDFs de preparação,
políticas gerais e por procedimento, convênios e perfis documentais de duas médicas chamadas Ana.
Cada fonte possui um assunto e um contexto de entidades; divida um material que misture contextos em fontes distintas.
Todos os nomes, formulários e condições são fictícios. Os PDFs não definem jejum ou medicamentos.

Importe o acervo na raiz do projeto, usando a configuração de embeddings acima:

```powershell
foreach ($doc in (Get-Content docs/knowledge/corpus.json -Raw | ConvertFrom-Json)) {
    $arguments = @('ingest', '-file', $doc.file, '-topic', $doc.topic)
    foreach ($field in @('exam', 'procedure', 'doctor')) {
        if ($doc.$field) { $arguments += @("-$field", $doc.$field) }
    }
    go run ./cmd/knowledge @arguments
    if ($LASTEXITCODE -ne 0) { throw "Falha na ingestão: $($doc.file)" }
}

go run ./cmd/knowledge search -question 'Qual formulário apresentar?' -topic preparation -exam 'Ultrassom abdominal'
go run ./cmd/knowledge search -question 'Qual protocolo apresentar?' -topic policy -procedure 'Biópsia de pele'
go run ./cmd/knowledge search -question 'Qual apresentação administrativa?' -topic doctor_profile -doctor 'Ana Souza'
```

`-topic` aceita `general`, `faq`, `policy`, `preparation`, `insurance` ou `doctor_profile`.
`-exam`, `-procedure` e `-doctor` aceitam nomes do acervo. Nomes são comparados sem distinção de maiúsculas ou acentos;
um nome exato tem prioridade, seguido por uma sequência parcial de palavras. Filtros são combinados por AND.
Uma entidade desconhecida retorna `[]`, sem pesquisar outro contexto. Ausência ou ambiguidade de entidade em preparo
ou perfil pede esclarecimento antes de recuperar trechos. Para políticas sem entidade, somente políticas gerais são
recuperadas; se só existirem políticas específicas, o paciente deve identificar o contexto.
O CLI retorna erro de contexto ambíguo; a conversa apresenta opções dos metadados,
sem mostrar o preparo de outra entidade.

Exemplos conversacionais na mesma sessão:

| Mensagem | Resultado esperado |
| --- | --- |
| Como preparo o ultrassom? | Qual ultrassom: abdominal ou da tireoide? |
| O abdominal | Trechos apenas do ultrassom abdominal, com página e linhas |
| Qual protocolo para biópsia de pele? | Política do procedimento, sem trazer a política de curativo |
| Quero conhecer o perfil da Dra. Ana | Qual Ana: Souza ou Lima? |
| Quais horários a Dra. Ana Souza tem? | Busca no catálogo e núcleo determinístico |

O modelo extrai o assunto e os nomes do pedido e do contexto; o serviço resolve esses nomes nos metadados persistidos.
Filtros não são inferidos por similaridade vetorial. A qualidade da interpretação do pedido depende do modelo;
testes simulados verificam o contrato e os efeitos determinísticos, sem demonstrar acerto semântico universal.

Para atualizar uma fonte, edite seu arquivo e execute novamente sua ingestão, mantendo o caminho e informando os
metadados atuais. Metadados omitidos voltam a `general`, sem entidades. Falha de extração, embeddings, cancelamento
ou gravação preserva a versão anterior completa. Chamadas externas ocorrem antes do lock de importação.

Os PDFs podem ser regenerados com `python scripts/generate-knowledge-pdfs.py` em um ambiente com `reportlab`.
O parser de produção usa [ledongthuc/pdf](https://github.com/ledongthuc/pdf), sem executáveis externos ou OCR.

## FAQ fictício de convênios

O arquivo [faq-convenios-simulated.md](faq-convenios-simulated.md) apresenta planos inventados, modalidades de
atendimento, documentos, autorização, coparticipação e reembolso. Todas as condições fazem parte da demonstração.
Importe esse arquivo para acrescentá-lo ao mesmo acervo. As perguntas abaixo pesquisam todos os documentos
ingeridos, incluindo o FAQ geral:

```powershell
$convenios = go run ./cmd/knowledge ingest -file docs/knowledge/faq-convenios-simulated.md | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { throw 'Falha na ingestão do FAQ de convênios' }

go run ./cmd/knowledge search -question 'Quais planos vocês aceitam?' -limit 3
go run ./cmd/knowledge search -question 'O Ipê Azul Básico inclui exames?' -limit 3
go run ./cmd/knowledge search -question 'Meu plano não está na lista. Posso pagar?' -limit 3
```

Confira, respectivamente, os trechos sobre convênios aceitos, cobertura do Ipê Azul Básico e atendimento particular.
Perguntas sem valor definido no FAQ, como o preço da coparticipação, devem recuperar a orientação de consulta à
recepção; o documento não fornece um valor para ser inferido. O comando continua apresentando somente trechos.

## Outros FAQs fictícios por tema

Os documentos abaixo ampliam o material da mesma clínica fictícia. Cada pergunta e sua resposta permanecem no
mesmo parágrafo para que a ingestão preserve o contexto do trecho. Informações compartilhadas, como horário,
endereço e formas de pagamento, seguem o FAQ geral e o FAQ de convênios.

- [Atendimento](faq-atendimento-simulated.md): recepção, primeira visita, cadastro, atrasos e comparecimento.
- [Agendamentos](faq-agendamentos-simulated.md): disponibilidade, propostas, confirmação, remarcação e cancelamento.
- [Pagamentos](faq-pagamentos-simulated.md): formas de pagamento, preços, parcelamento, recibos e dúvidas de cobrança.
- [Acesso e acessibilidade](faq-acessibilidade-simulated.md): localização, rampa, estacionamento e apoio para a visita.

Importe cada arquivo desejado separadamente, usando o mesmo procedimento do FAQ de convênios. Por exemplo:

```powershell
$atendimento = go run ./cmd/knowledge ingest -file docs/knowledge/faq-atendimento-simulated.md | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { throw 'Falha na ingestão do FAQ de atendimento' }

go run ./cmd/knowledge search -question 'Vocês abrem aos domingos?' -limit 3
go run ./cmd/knowledge search -question 'Com quanto tempo de antecedência chego?' -limit 3
```

Para conferir os demais documentos, use perguntas com palavras diferentes das usadas nos títulos:

| FAQ | Pergunta de verificação | Trecho esperado |
| --- | --- | --- |
| Atendimento | Vocês abrem aos domingos? | Horário de atendimento da recepção |
| Atendimento | Com quanto tempo de antecedência chego? | Chegada antes do horário marcado |
| Agendamentos | Escolher uma vaga já deixa a consulta marcada? | Preferência de horário não reserva a vaga |
| Agendamentos | Se não conseguir trocar, continuo com a reserva antiga? | Horário original preservado |
| Pagamentos | Posso pagar com cartão de débito? | Formas de pagamento aceitas |
| Pagamentos | Qual é o preço da consulta? | Valor informado pela recepção; FAQ sem tabela de preços |
| Acessibilidade | Preciso subir escadas para chegar à recepção? | Entrada com rampa e recepção no térreo |
| Acessibilidade | Onde deixo o carro e quanto custa? | Estacionamento gratuito e entrada de veículos |

As perguntas são exemplos para inspeção manual, sem garantia de ranking do fornecedor real. Importe todos os FAQs
que deseja pesquisar e faça as perguntas sem informar o documento. Uma pergunta pode recuperar trechos de fontes
diferentes. Os exemplos sobre preços e recursos não confirmados permitem conferir se há suporte na fonte.

## Extração, chunking e falhas

- Formatos aceitos: `.md` e `.txt`, UTF-8, até 64 KiB; PDF textual até 1 MiB, 32 páginas e 64 KiB de texto extraído.
  PDF inválido, criptografado, binários, UTF-8 inválido, falha de leitura ou excesso de tamanho
  retornam erro de extração.
  nenhum embedding é solicitado para esses casos. PDF sem texto retorna `document contains no text`; não há OCR.
- Um documento sem texto retorna `document contains no text`. A extração remove BOM UTF-8 inicial e normaliza
  CRLF/CR para LF, preservando os números de linha da fonte. Markdown é mantido como texto, incluindo seus títulos.
- Parágrafos separados por linhas em branco formam chunks de até 1000 caracteres Unicode, sem overlap de conteúdo.
  Linhas longas são divididas; nesse caso, chunks diferentes podem citar a mesma linha. O limite é de 256 chunks.
- Chunks de PDF nunca atravessam páginas. `page` identifica a página original a partir de 1, inclusive após páginas
  vazias. Linhas de PDF se referem ao texto extraído daquela página,
  não a coordenadas visuais ou linhas do arquivo binário.
- Embeddings são gerados antes da transação. Falha do fornecedor, quantidade incorreta de vetores, dimensão errada,
  NaN, infinito e vetores zero interrompem a ingestão. Documento e chunks são gravados em uma única transação;
  falha de banco desfaz toda a gravação. Contexto cancelado e `COMMAND_TIMEOUT` interrompem a operação.
- O acervo usa uma única configuração de provider, modelo e dimensões. O primeiro documento importado estabelece
  essa configuração. Ingestões e buscas incompatíveis retornam `embedding configuration differs within the clinic
  corpus`; alterar a `.env` não converte vetores existentes. Dados legados misturados também causam esse erro,
  sem pesquisar silenciosamente apenas uma parte do acervo. O banco verifica ainda as dimensões de cada chunk.
- Para mudar o modelo ou as dimensões, reconstrua o acervo inteiro com uma única configuração. Reimportar com outra
  configuração no mesmo acervo é rejeitado; esta entrega não oferece conversão nem limpeza automática.
  Documentos anteriores compatíveis continuam funcionando sem nova migration.
- Um acervo vazio retorna `[]` sem solicitar embeddings. Pergunta vazia, maior que 1000 caracteres Unicode ou limite
  fora de 1..100 são rejeitados. Erros saem em stderr e o processo termina com código diferente de zero.

A busca faz varredura exata com distância de cosseno no PostgreSQL; esta fatia não adiciona índices aproximados.
Conteúdo e localização vêm da fonte persistida. Alterar o arquivo local após a ingestão não altera o documento salvo.

## Avaliar qualidade da recuperação

O [executor de recuperação](../evals/retrieval.md) mede recall@k com perguntas e fontes rotuladas manualmente.
Usa um acervo de avaliação isolado e registra embedding, configuração, resultados relevantes e fontes ausentes.
Um fixture demonstra o cálculo sem API; o modo real usa a ingestão e busca de produção com PostgreSQL.

## Verificar sem credenciais externas

```powershell
.\scripts\test-integration.ps1 -run TestKnowledge -v
.\scripts\test-unit.ps1 -run TestEmbeddings -v
```

Os testes usam PostgreSQL/pgvector real em `TEST_DATABASE_URL`, com um schema isolado por teste, e embeddings
controlados de três dimensões. Verificam ranking global entre documentos, citações, acervo vazio, extração,
incompatibilidades legadas, ingestões concorrentes e rollback após falhar a inserção do segundo chunk.
Os testes HTTP usam um provedor local simulado para validar o contrato, erros e respostas malformadas.
Não demonstram a qualidade semântica do fornecedor real; as perguntas do roteiro permitem inspecioná-la manualmente.

## Coordenação

`knowledge.Service` sequencia extração → chunking → validação do acervo → embeddings → gravação atômica.
O repositório PostgreSQL adquire `SHARE ROW EXCLUSIVE` na tabela de documentos durante a transação e confere de novo
sua configuração antes de inserir. Isso serializa ingestões, inclusive as primeiras importações concorrentes,
sem manter locks durante chamadas externas nem bloquear buscas comuns. Se duas primeiras ingestões tiverem
configurações diferentes, uma conclui e a outra retorna incompatibilidade; ambas podem já ter gerado embeddings.

Na busca, o módulo confere a configuração do acervo antes de gerar o vetor da pergunta. A consulta SQL repete a guarda
de compatibilidade no mesmo snapshot da comparação e ordena os chunks de todos os documentos. O caminho normal
faz duas consultas ao banco e uma chamada ao fornecedor. Uma consulta sem resultados reconfere a configuração para
distinguir incompatibilidade de ausência de correspondência. Busca filtrada carrega também o catálogo de metadados
para resolver entidades antes dos embeddings. Falhas na substituição fazem rollback da remoção e da nova gravação.
Importações da mesma fonte são serializadas; a última transação concluída define a versão atual. Buscas concorrentes
observam uma versão completa no snapshot da consulta SQL. O histórico de conversa já exibido continua sendo histórico;
citações do histórico nunca são aceitas como evidência de uma nova recuperação.

## Respostas conversacionais com fontes (#21)

Importe os FAQs acima e inicie `go run ./cmd/clinic serve`. O servidor usa `EMBEDDING_MODEL` e
`EMBEDDING_DIMENSIONS` com os mesmos padrões do comando de ingestão; mantenha a configuração compatível com o acervo.
Na sessão autenticada descrita em [agent-local.md](../agent-local.md), envie estas mensagens a
`POST /v1/agent/messages`, com um novo `message_id` UUID por mensagem,
preservando o `conversation_id` e enviando `conversation_revision` da última resposta:

| Mensagem | Resultado esperado |
| --- | --- |
| Posso estacionar na clínica? | Trecho sobre estacionamento, com documento e linhas da fonte |
| Vocês têm piscina? | Ausência de suporte nos materiais, sem inferir uma resposta negativa |
| Quero horários de cardiologia na próxima semana | Disponibilidade real pelo núcleo de agendamento |

Perguntas operacionais consultam até três trechos de todo o acervo. O modelo seleciona os `excerpt_ids` que sustentam
diretamente a pergunta; similaridade não garante suporte. Uma lista vazia indica abstention. O servidor valida cada ID
contra a última recuperação do turno e apresenta o texto integral do trecho, sem acrescentar fatos do modelo.
As citações em `message` incluem nome da fonte, UUID do documento, ordinal do trecho, linhas e página quando houver.
O modelo não fornece nomes de fontes, localizações ou texto factual para renderização. IDs desconhecidos, repetidos,
fontes apenas do histórico e campos adicionais na resposta são rejeitados com `MODEL_INVALID_RESPONSE`.

Esta entrega usa respostas extrativas para preservar as condições e ressalvas do material. A seleção semântica de um
trecho relevante ainda depende do modelo; os testes simulados não demonstram sua qualidade com o fornecedor real.
Importe materiais administrativos em português e confira as respostas com as fontes. A
[avaliação de respostas RAG](../evals/rag-answers.md) verifica respostas extrativas gravadas, groundedness,
citações e abstention com rótulos manuais. Os fixtures não demonstram a qualidade de um modelo real.

Acervo vazio ou ausência de suporte produz: “Não encontrei essa informação nos materiais da clínica.”
Falha de banco, embeddings, incompatibilidade do acervo ou serviço não configurado produz:
“Não consegui consultar os materiais da clínica agora. Tente novamente mais tarde.”
Esses resultados são respostas HTTP 200, com `appointments: []`, sem proposta nem disponibilidade.
Uma falha descarta inclusive resultados parciais e não solicita síntese ao modelo; detalhes internos não são exibidos.
Cancelamento do contexto interrompe o turno e preserva o tratamento de erro existente.

O agente mantém o acesso exclusivo à conversa durante recuperação e seleção, como nas demais ferramentas.
O fluxo continua responsável por consumir a autoridade de propostas antes do modelo, aplicar a intenção e salvar
histórico somente após uma resposta válida. Consultar o acervo não confirma nem cria agendamentos; falha de validação
ou cancelamento preserva o rollback do turno e não restaura autoridade consumida. Trechos e validadores permanecem
locais ao turno, sem referências de fontes persistidas como autorização. Durante a seleção de evidências documentais,
nenhuma ferramenta fica disponível e qualquer tool call é rejeitada antes da execução. Instruções no documento não
alteram regras, não autorizam mutações e não podem remover citações; o servidor controla texto e localização exibidos.
Médicos, especialidades, vagas e reservas
continuam vindo das ferramentas determinísticas; aconselhamento médico continua fora do escopo.
