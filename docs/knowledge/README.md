# Ingestão e recuperação de documentos da clínica

A entrega de #20 demonstra recuperação antes da integração ao agente. O operador importa um FAQ e pesquisa
trechos classificados por similaridade de cosseno, com documento, texto e linhas para conferência da fonte.

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

go run ./cmd/knowledge search -document $imported.id -question 'Posso estacionar na clínica?' -limit 3
go run ./cmd/knowledge search -document $imported.id -question 'A recepção abre no sábado?' -limit 3
```

Cada resultado JSON contém `document_id`, `source`, `chunk` (ordinal a partir de 1), `text`, `start_line`,
`end_line` e `similarity`. Similaridade maior aparece primeiro; empates seguem a ordem dos chunks na fonte.
Na primeira pergunta, confira o trecho “Há estacionamento?”; na segunda, “Qual é o horário de atendimento?”.
Os valores de similaridade dependem do modelo; não são probabilidades nem uma garantia de relevância.
O comando apresenta trechos recuperados, sem compor uma resposta clínica ou aplicar limiar de abstention.

Guarde o `id` retornado para pesquisar após reiniciar o processo. Cada importação cria um documento novo e imutável;
reimportar não substitui documentos anteriores. A busca usa somente o documento escolhido.

## FAQ fictício de convênios

O arquivo [faq-convenios-simulated.md](faq-convenios-simulated.md) apresenta planos inventados, modalidades de
atendimento, documentos, autorização, coparticipação e reembolso. Todas as condições fazem parte da demonstração.
Importe esse arquivo separadamente e use o ID retornado para consultar seus trechos:

```powershell
$convenios = go run ./cmd/knowledge ingest -file docs/knowledge/faq-convenios-simulated.md | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { throw 'Falha na ingestão do FAQ de convênios' }

go run ./cmd/knowledge search -document $convenios.id -question 'Quais planos vocês aceitam?' -limit 3
go run ./cmd/knowledge search -document $convenios.id -question 'O Ipê Azul Básico inclui exames?' -limit 3
go run ./cmd/knowledge search -document $convenios.id -question 'Meu plano não está na lista. Posso pagar?' -limit 3
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

go run ./cmd/knowledge search -document $atendimento.id -question 'Vocês abrem aos domingos?' -limit 3
go run ./cmd/knowledge search -document $atendimento.id -question 'Com quanto tempo de antecedência chego?' -limit 3
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

As perguntas são exemplos para inspeção manual, sem garantia de ranking do fornecedor real. Use sempre o ID do
documento correspondente: importar vários arquivos não habilita uma busca conjunta entre eles. Os exemplos sobre
preços e recursos não confirmados também permitem verificar se a fonte realmente contém a informação procurada.

## Extração, chunking e falhas

- Formatos aceitos: `.md` e `.txt`, UTF-8, até 64 KiB. PDF, binários, UTF-8 inválido, falha de leitura ou excesso de
  tamanho retornam `document extraction failed`; nenhum embedding é solicitado para esses casos.
- Um documento sem texto retorna `document contains no text`. A extração remove BOM UTF-8 inicial e normaliza
  CRLF/CR para LF, preservando os números de linha da fonte. Markdown é mantido como texto, incluindo seus títulos.
- Parágrafos separados por linhas em branco formam chunks de até 1000 caracteres Unicode, sem overlap de conteúdo.
  Linhas longas são divididas; nesse caso, chunks diferentes podem citar a mesma linha. O limite é de 256 chunks.
- Embeddings são gerados antes da transação. Falha do fornecedor, quantidade incorreta de vetores, dimensão errada,
  NaN, infinito e vetores zero interrompem a ingestão. Documento e chunks são gravados em uma única transação;
  falha de banco desfaz toda a gravação. Contexto cancelado e `COMMAND_TIMEOUT` interrompem a operação.
- Provider, modelo e dimensões ficam registrados por documento. Uma busca com configuração diferente retorna
  `embedding configuration differs from the document`; reimporte o documento para usar outra configuração.
  O banco também verifica dimensões por chunk e sua correspondência com o documento.
- Documento desconhecido retorna `document not found`. Pergunta vazia, maior que 1000 caracteres Unicode, UUID
  inválido ou limite fora de 1..100 são rejeitados. Erros saem em stderr e o processo termina com código
  diferente de zero.

A busca faz varredura exata com distância de cosseno no PostgreSQL; esta fatia não adiciona índices aproximados.
Conteúdo e localização vêm da fonte persistida. Alterar o arquivo local após a ingestão não altera o documento salvo.

## Verificar sem credenciais externas

```powershell
.\scripts\test-integration.ps1 -run TestKnowledge -v
.\scripts\test-unit.ps1 -run TestEmbeddings -v
```

Os testes usam PostgreSQL/pgvector real em `TEST_DATABASE_URL`, com um schema isolado por teste, e embeddings
controlados de três dimensões. Verificam persistência, ranking conhecido, citações, extração e incompatibilidades.
Os testes HTTP usam um provedor local simulado para validar o contrato, erros e respostas malformadas.
Não demonstram a qualidade semântica do fornecedor real; as perguntas do roteiro permitem inspecioná-la manualmente.

## Coordenação

`knowledge.Service` sequencia extração → chunking → embeddings → gravação atômica. O repositório PostgreSQL mantém
a transação somente durante a gravação, sem chamada externa sob lock. Na busca, o serviço valida a entrada, consulta
somente a configuração persistida e a confere antes de gerar o embedding da pergunta. A consulta dos chunks também
filtra a configuração no SQL antes de comparar vetores, sem repetir a leitura de metadados quando há resultados.
Documentos são imutáveis nesta entrega. Falhas anteriores à gravação não criam estado; falhas durante a gravação
fazem rollback.
