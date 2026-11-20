# Avaliação de recuperação (#23)

`retrieval-eval` mede a busca, sem gerar respostas ou usar o modelo conversacional.
[retrieval.pt-BR.json](retrieval.pt-BR.json) contém um acervo fictício fechado, perguntas em português,
filtros opcionais e relevância manual. Os textos estão dentro do dataset: alterações no acervo operacional
não afetam esta avaliação. `source` é uma identidade, não um caminho aberto pelo executor.

## Definição de recall@k

Cada alvo de `relevant` identifica uma fonte inteira (`source`) ou exatamente um trecho (`source` e `chunk`,
ordinal desde 1). Para uma fonte inteira, qualquer trecho dela é relevante. Para um trecho, só aquele ordinal conta.
É permitido esperar vários trechos de uma fonte ou várias fontes. Rótulos duplicados ou sobrepostos são rejeitados:
não misture fonte inteira e trechos da mesma fonte na mesma pergunta.

```text
recall@k = alvos relevantes distintos nos primeiros k resultados / total de alvos relevantes esperados
```

k conta trechos, inclusive irrelevantes ou vários trechos do mesmo documento. Cada alvo relevante conta uma vez.
O denominador não é limitado por k: dois alvos esperados com um recuperado produzem 0,5, mesmo quando k = 1.
Os resultados mantêm a ordem da busca, com posições desde 1. Não há limiar adicional de similaridade.
Similaridade não é probabilidade nem prova de relevância.

`mean_recall_at_k` é a média aritmética entre perguntas com resposta conhecida, com igual peso por pergunta.
Uma pergunta com `no_answer: true` deve ter `relevant: []`; a ausência de resposta se refere a este acervo.
Seu recall é `null` e ela fica fora da média. Se todas forem assim, a média também é `null`.
`no_answer_with_results` conta perguntas sem resposta que recuperaram material; todos esses resultados são
irrelevantes. Isso mede ruído recuperado, sem afirmar que um gerador responderia indevidamente.

## Verificar o cálculo sem serviços externos

Na raiz do projeto:

```powershell
New-Item -ItemType Directory -Force .scratch | Out-Null
go run ./cmd/retrieval-eval -output .scratch/retrieval.fixture.json
if ($LASTEXITCODE -ne 0) { throw 'Falha na avaliação' }
```

O modo padrão `fixture` usa [retrieval-rankings.fixture.json](retrieval-rankings.fixture.json), sem banco ou API.
As posições controladas estão vinculadas ao SHA-256 do dataset. Alterar perguntas, rótulos, metadados ou textos
exige outro fixture com fingerprint atual e rankings revisados. Ele demonstra o cálculo, sem medir embeddings reais.

Com k = 3, há oito perguntas com resposta e três sem resposta; o recall médio conhecido é 0,875.
`documentos` e `visita` recuperam um de dois alvos nos três primeiros resultados: recall 0,5 cada.
O segundo alvo está na quarta posição, fora do corte. As outras seis perguntas respondíveis têm recall 1.
`piscina` e `preco` recuperam ruído; `academia` não retorna material. `preco` pede um número exato ausente:
a orientação para consultar a recepção não fornece esse número.

## Executar a recuperação real

Configure `.env` com `OPENAI_API_KEY` e `TEST_DATABASE_URL`. O modo real envia documentos e perguntas à OpenAI
e pode gerar cobrança. Execute explicitamente:

```powershell
docker compose --profile test up -d --wait test-db
go run ./cmd/retrieval-eval -mode real -k 3 -output .scratch/retrieval.real.json
if ($LASTEXITCODE -ne 0) { throw 'Falha operacional; não interpretar como aprovação' }
```

`RETRIEVAL_EVAL_DATABASE_URL` substitui `TEST_DATABASE_URL`. A URL deve ser PostgreSQL e apontar para um nome
de banco diferente de `DATABASE_URL` (padrão da aplicação: `clinic`). O usuário precisa criar e remover schemas,
executar migrations e dispor de pgvector. Cada execução cria um schema aleatório exclusivo; o pool usa somente
esse schema, sem fallback para tabelas públicas. Nenhum material de outro acervo é pesquisado.

O executor ingere os documentos, verifica os ordinais esperados e pesquisa pela interface `knowledge.Service.Search`,
com os filtros declarados. Usa o repositório PostgreSQL de produção: cosseno exato, filtros antes do ranking global,
desempate por UUID de documento e ordinal. UUIDs são novos a cada execução; empates podem trocar posições.
O chunking usa parágrafos de até 1000 caracteres Unicode, sem overlap e com normalização CRLF/CR para LF.
O dataset aceita textos UTF-8 `.md`/`.txt` de até 64 KiB, não PDFs.

Padrões: `-model text-embedding-3-small`, `-dimensions 1536`, `-k 3` e `-timeout 2m`.
Modelo e dimensões são flags; não são lidos de `EMBEDDING_MODEL` ou `EMBEDDING_DIMENSIONS`.
Compare execuções com o mesmo acervo, rótulos e k. Inspecione também filtros e empates.
Os resultados variam com o fornecedor e não garantem qualidade fora do dataset.

A sequência é ingestão → validação de alvos → buscas → relatório. Não há locks durante chamadas de embeddings;
gravações conservam a transação e o rollback do módulo de conhecimento. Em sucesso ou falha, o executor fecha
o pool e remove o schema com contexto de limpeza independente do prazo. Falha de limpeza também encerra com erro.
Encerramento forçado pode deixar um schema `retrieval_eval_*` para remoção manual. A extensão pgvector permanece.

## Inspecionar relatórios

O JSON inclui acervo completo, identificador e SHA-256, SHA-256 do dataset, embedding, modo, algoritmo, chunking,
k, prazo, revisão Git e indicação de workspace modificado. Fingerprints usam a serialização JSON compacta dos
tipos carregados, sem depender da indentação. Sem Git, a revisão é `unavailable`. O modo fixture identifica
embedding `fixture/manual-rankings-v1`, com zero dimensões; não afirma ter executado um embedding.

Cada pergunta inclui texto, filtros, alvos esperados, recall, alvos ausentes e resultados com posição, relevância,
trecho, similaridade e localização. Em modo real, inclui UUID e linhas do serviço. Os agregados guardam IDs:

```powershell
$report = Get-Content .scratch/retrieval.real.json -Raw | ConvertFrom-Json
$report.configuration
$report.totals
$report.questions | Where-Object { $_.id -in $report.totals.with_missing } | ConvertTo-Json -Depth 20
$report.questions | Where-Object { $_.id -in $report.totals.with_irrelevant } | ConvertTo-Json -Depth 20
```

Saída 0 significa execução concluída, mesmo com recall baixo; não há limiar de aprovação implícito.
Saída 2 indica entrada inválida ou falha operacional. Erros de embeddings, banco, ambiguidade de filtro e
cancelamento interrompem a execução sem publicar métricas parciais nem substituir o arquivo de saída existente.

Os testes de integração do comando usam banco real e embeddings controlados por servidor HTTP local.
Cobrem execução completa, ranking, relevância, configuração e limpeza após sucesso e falha.
Não substituem uma execução explícita com o fornecedor real.
