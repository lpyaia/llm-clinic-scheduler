# Comparação de estratégias RAG (#25)

`rag-compare` executa uma matriz de chunking e top-k sobre um único snapshot de documentos, perguntas e referências.
Combina ingestão/busca por `knowledge.Service`, seleção/renderização extrativa de produção e a rubrica de
fundamentação/citações de [rag-eval](rag-answers.md). A geração recebe os trechos efetivamente recuperados.

## Comparação reproduzível sem serviços

Na raiz do projeto:

```powershell
New-Item -ItemType Directory -Force .scratch | Out-Null
go run ./cmd/rag-compare -output .scratch/rag-comparison.fixture.json
```

O dataset [rag-strategies.pt-BR.json](rag-strategies.pt-BR.json) contém quatro documentos fictícios de mais de
1.300 caracteres e nove perguntas, incluindo duas sem resposta. Os documentos usam `lines` com texto UTF-8
normalizado, sem BOM ou quebras dentro de uma linha. As referências incluem condições e múltiplas fontes.

A matriz padrão [rag-matrix.json](rag-matrix.json) combina parágrafos de até 600 runes e janelas de 300 runes
com sobreposição de 60, com top-k 1 e 3. `-dataset` e `-matrix` aceitam outros snapshots e matrizes.
Uma matriz exige pelo menos duas estratégias distintas e dois valores distintos de top-k; k aceita 1..100.
Tamanhos de chunks aceitam 1..1000 runes. Sobreposição deve ser menor que o tamanho e só é válida em janelas.
O formato de resposta de produção seleciona no máximo três trechos, mesmo quando top-k for maior.

O modo `fixture` não carrega `.env`, não usa rede e não precisa de PostgreSQL.
O embedding `lexical-v1` conta seis termos fixos e adiciona um bias não nulo, em sete dimensões.
Os termos são `estacionamento`, `rampa`, `documento`, `ultrassom`, `jejum` e `convênio`.
O modelo `first-three-v1` escolhe mecanicamente os primeiros três trechos recuperados, inclusive os irrelevantes.
Esses provedores controlados permitem verificar efeitos conhecidos; não demonstram qualidade semântica real.

| Estratégia | k | Chunks no índice | Recall médio | Resposta correta | Fundamentação / citações |
| --- | --- | --- | --- | --- | --- |
| Parágrafos 600 | 1 | 16 | 35,71% | 2/9 | 100% / 100% |
| Parágrafos 600 | 3 | 16 | 71,43% | 0/9 | 100% / 100% |
| Janelas 300, overlap 60 | 1 | 24 | 42,86% | 2/9 | 100% / 100% |
| Janelas 300, overlap 60 | 3 | 24 | 71,43% | 1/9 | 100% / 100% |

Recall exclui as duas perguntas sem resposta: denominador de sete cenários, com peso igual por pergunta.
No fixture, todas as perguntas geram texto; os denominadores de resposta, fundamentação e citações são nove.
A decisão de abstenção acerta 7/9 em todas as células. Há zero falhas operacionais e zero afirmações sem suporte.

**Conclusão desta comparação controlada:** aumentar k melhora recall nas duas estratégias, mas a seleção mecânica
também inclui conteúdo irrelevante e reduz a correção das respostas. Fundamentação e citações perfeitas não garantem
que a resposta satisfaça a pergunta. Esses resultados validam o executor e não escolhem uma configuração de produção.
Documentos mais longos e perguntas idênticas em todas as células evitam uma comparação sem efeito de chunking.

## Executar embeddings e geração reais

Configure `OPENAI_API_KEY` e `OPENAI_MODEL` no ambiente ou na `.env`, conforme [agent-local.md](../agent-local.md).
A execução real envia somente o corpus e as perguntas do dataset aos adapters existentes da OpenAI e pode gerar custo.

```powershell
go run ./cmd/rag-compare -mode real -output .scratch/rag-comparison.real.json
```

`-model` substitui o modelo de geração configurado. Embeddings usam `-embedding-model text-embedding-3-small`
e `-dimensions 1536` por padrão. Flags de provedor exigem modo real explícito; não são ignoradas em modo fixture.
`-timeout 10m` limita toda a matriz. O executável mantém timeout HTTP de 45 segundos por chamada.
Prompts, respostas estruturadas, uso informado e configuração efetiva de geração ficam no relatório.
Não há julgamento semântico por outro modelo. Repetições de geração real podem produzir escolhas diferentes.

Uma execução real em 20/11/2026, com `text-embedding-3-small` (1536 dimensões) e `gpt-6-luna`, produziu:

| Estratégia | k | Recall médio (7 perguntas) | Respostas corretas (9 perguntas) |
| --- | --- | --- | --- |
| Parágrafos 600 | 1 | 78,57% | 6/9 |
| Parágrafos 600 | 3 | 92,86% | 7/9 |
| Janelas 300, overlap 60 | 1 | 78,57% | 6/9 |
| Janelas 300, overlap 60 | 3 | 100% | 8/9 |

Todas as unidades respondidas tiveram fundamentação/citações 100%, com zero falhas operacionais.
Os denominadores dessas duas métricas foram sete, sete, seis e sete cenários, respectivamente: as demais respostas
foram abstenções e ficaram fora da média. A configuração de janelas com k=1 teve uma abstenção indevida.

O [resumo versionado](rag-strategies.real-summary.json) preserva configurações, fingerprints e resultados por cenário.
O relatório completo com prompts e respostas foi salvo localmente em `.scratch/rag-comparison.real.json`.
Essa execução ocorreu com workspace modificado; a revisão de base e o fingerprint do executável estão registrados.
Houve uma geração por pergunta/configuração, sem estimativa estatística de variabilidade.

**Conclusão da execução real:** janelas com k=3 foram a melhor configuração observada neste corpus.
O ganho de recall permitiu cobrir as duas referências de preparo e combinar fontes; a falha restante foi seleção
de conteúdo adicional não solicitado na pergunta sobre documentos. Esse resultado sustenta investigar essa
configuração em um corpus maior e com repetições, sem trocar automaticamente a configuração da aplicação.

O índice do experimento é um índice vetorial exato em memória, com cosseno, filtros de metadados antes do ranking
e desempate por fonte/ordinal. Cada célula recria o índice e reingere o mesmo snapshot, inclusive quando só k muda.
Índices não são compartilhados entre células, não consultam o acervo da aplicação e são descartados após o uso.
Isso permite comparar embeddings/chunking/geração sem banco. O ranking PostgreSQL continua avaliado em
[retrieval.md](retrieval.md); esta comparação não mede desempenho do banco ou de índices aproximados.
Reranking não é usado e fica explicitamente registrado como `none`.

## Referências estáveis e métricas

Cada pergunta declara `id`, `text`, `reference`, `filter` opcional, `expected` e `no_answer`.
Cada evidência esperada identifica uma fonte e um texto literal que deve ocorrer uma única vez no documento.
Não use ordinais de chunks como referência: eles mudam quando a estratégia muda.
`no_answer: true` exige `expected: []`; os demais casos exigem ao menos uma evidência.

O executor localiza as referências em intervalos de runes do documento original e usa a união dos intervalos
dos chunks recuperados para medir cobertura completa. Uma referência dividida entre chunks exige todos os pedaços.
Janelas sobrepostas não contam a mesma referência várias vezes. A correção da resposta exige cobrir todas as
referências e não incluir unidades de afirmação sem relação com elas. Esse critério é de cobertura extrativa,
não de entailment semântico geral. Preservar o contexto necessário depende da qualidade dos rótulos manuais.
Somente as identidades efetivamente selecionadas pelo modelo fornecem cobertura à resposta;
outras cópias do mesmo texto no índice não preenchem uma referência ausente.

Groundedness, citações e afirmações sem suporte usam a rubrica extrativa compartilhada com `rag-eval`.
Abstenção e falhas operacionais mantêm notas de fundamentação/citações `null` e não aumentam essas médias.
Cada métrica agregada inclui `value` e `count`, o denominador de cenários com valor aplicável.
Falhas de geração são registradas por cenário; erros de protocolo são reprovações de comportamento.

O relatório inclui acervo, perguntas, parâmetros normalizados, fingerprints SHA-256 do dataset/matriz/índices,
revisão Git, indicação de workspace modificado, configuração dos provedores, índice completo e resultados por cenário.
Os UUIDs apresentados são identidades determinísticas de relatório, derivadas da célula e fonte;
não são IDs persistidos.
`comparisons` mostra diferenças em relação à primeira célula, calculadas somente entre pares do mesmo cenário com
métrica aplicável nas duas configurações. O denominador pareado fica em `count`; lacunas não são imputadas como zero.
As diferenças nas contagens de aprovados, reprovados, erros e afirmações sem suporte
usam o conjunto completo de cenários.

A sequência é validação → índice novo → ingestão → buscas → geração → métricas → comparação.
O comparador controla a ordem; ingestão e seleção de evidências conservam suas próprias validações.
Não há locks de conversa durante o experimento, mudanças em reservas ou gravações no corpus da aplicação.
O caminho conversacional existente mantém os locks e a ordem de execução anteriores.

Saída 0 significa comparação concluída, inclusive com configurações de baixa qualidade; não há aprovação implícita.
Saída 2 indica configuração inválida, falha operacional ou de escrita. Cancelamento e falha de embeddings/busca
interrompem sem publicar métricas parciais. Entrada inválida preserva o arquivo de relatório existente.
Compile `go build -o bin/rag-compare.exe ./cmd/rag-compare` para obter os códigos originais fora do wrapper de `go run`.

Os testes conferem matriz 2×2 com recall conhecido, cobertura de referências entre chunks, Unicode, sobreposição,
isolamento, reprodução idêntica, cancelamento e falhas dos dois provedores por HTTP local, sem execução paga em CI.
