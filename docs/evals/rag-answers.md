# Avaliação de respostas RAG (#24)

`rag-eval` avalia respostas extrativas gravadas, com rótulos manuais e acervo fictício fechado.
Não acessa banco, credenciais ou rede. A recuperação continua avaliada separadamente em
[retrieval.md](retrieval.md); esta avaliação considera o texto respondido, suas fontes e a abstention.

## Executar os exemplos conhecidos

Na raiz do repositório:

```powershell
New-Item -ItemType Directory -Force .scratch | Out-Null
go run ./cmd/rag-eval -output .scratch/rag-answers.fixture-report.json
```

O conjunto padrão tem 12 cenários: **3 aprovados, 8 reprovados e 1 erro operacional deliberado**.
A saída do executável é 2 por causa desse erro, mesmo com relatório completo. Isso é esperado neste fixture,
que comprova a detecção de negativos; não representa aprovação da qualidade de um modelo.
Compile com `go build -o bin/rag-eval.exe ./cmd/rag-eval` para observar os códigos originais sem o wrapper de `go run`.
Os testes do comando conferem essas classificações e a geração do JSON por cenário.

| Cenário | Classificação esperada |
| --- | --- |
| `parking-correct` | Resposta fundamentada, correta e com citação correta |
| `visit-two-sources` | Duas fontes corretas, incluindo localização em PDF |
| `pool-correct-abstention` | Abstention correta diante de material sem resposta |
| `plausible-unsupported` | Preço plausível inventado: afirmação sem suporte e resposta incorreta |
| `invented-citation` | Trecho citado inexistente no acervo |
| `source-not-retrieved` | Fonte existente, ausente da recuperação atual; afirmação sem suporte recuperado |
| `wrong-citation` | Texto correto, mas a fonte citada não sustenta a afirmação |
| `wrong-location` | Página incorreta em uma fonte existente e recuperada |
| `irrelevant-grounded-answer` | Texto fiel e citação correta que não respondem à pergunta |
| `undue-abstention` | Abstention indevida apesar do material esperado recuperado |
| `missing-citation` | Resposta correta e fundamentada sem indicação de fonte |
| `retrieval-unavailable` | Falha operacional, sem métricas de qualidade nem aprovação |

## Formato e captura de observações

[rag-answers.pt-BR.json](rag-answers.pt-BR.json) contém `version: 1`, `corpus` e `scenarios`.
O acervo guarda os `knowledge.Result` completos: identidade UUID, fonte, ordinal, texto, linhas e página opcional.
Cada cenário declara `id`, `question`, `reference_answer`, `expected_evidence` e `abstention_expected`.
IDs de evidência usam `UUID:ordinal`. Os rótulos devem indicar todos os trechos necessários e suficientes para
responder à pergunta, preservando condições e ressalvas. A referência em linguagem natural é inspecionável;
os trechos esperados são a rubrica executável. Similaridade não participa do julgamento.
Abstention esperada exige lista de evidências esperadas vazia; os outros casos exigem ao menos um trecho.

[rag-responses.fixture.json](rag-responses.fixture.json) guarda `dataset_sha256`, `provider`, `model` e `responses`.
Cada resposta, identificada pelo ID do cenário, contém `retrieved`, `message_lines` e `error` opcional.
`retrieved` lista exatamente os IDs da última recuperação do turno. Não inclua fontes somente do histórico.
`message_lines` conserva a mensagem completa, dividida em linhas para facilitar inspeção e respeitar a formatação.
Listas vazias devem ser explícitas. Falhas operacionais podem omitir essas listas e declarar `error`.

Para avaliar outra gravação, monte um dataset com o snapshot real dos trechos recuperados e os rótulos manuais.
Inclua também as fontes conhecidas necessárias para distinguir uma citação inexistente de uma não recuperada.
Capture a pergunta e a mensagem final de `Agent.ReplyConversation`, sem reconstruir o texto pelos IDs escolhidos.
Converta CRLF/CR para LF e divida `Result.Message` com `strings.Split(message, "\n")`; não remova texto adicional.
Registre o fornecedor e modelo que geraram a resposta, ou `fixture/manual-extractive-v1` para exemplos manuais.
`rageval.Dataset.Fingerprint()` calcula o SHA-256 do JSON compacto dos tipos carregados, incluindo textos e rótulos.
Esse fingerprint vincula a gravação ao dataset; mudanças exigem revisar as observações e atualizar o hash.

```powershell
go run ./cmd/rag-eval -dataset caminho/dataset.json -responses caminho/responses.json -output .scratch/rag.json
```

O executor valida os arquivos, classifica os cenários e serializa o relatório antes de abrir a saída.
Entrada inválida preserva um relatório existente. Nenhuma etapa adquire locks ou altera dados da aplicação.
Código 0 significa todos os cenários aprovados; 1 indica falha de comportamento; 2 indica entrada inválida,
falha de escrita ou erro operacional observado. Erros operacionais têm precedência sobre reprovações.
Com entrada válida, todos os cenários são relatados, inclusive quando algum deles contém erro operacional.

## Rubrica determinística `extractive-v1`

Esta rubrica acompanha o contrato atual: o servidor apresenta trechos integrais com citações, sem paráfrases.
Comparações de passagens ignoram somente espaços em branco nas extremidades; o conteúdo interno é preservado.
O cabeçalho fixo é removido; cada bloco anterior a uma linha `Fonte:` é uma unidade de afirmação.
Parágrafos internos e linhas `Fonte:` pertencentes ao texto conhecido do trecho ficam juntos.
Texto final sem citação também conta como unidade e não é descartado.

- `groundedness`: unidades cujo texto integral coincide com algum trecho **recuperado** / unidades respondidas.
  Não depende de qual fonte foi citada. `unsupported_claims` conserva o texto de cada unidade sem esse suporte.
- `answer_correct`: todas as evidências esperadas aparecem integralmente e não há unidade extra que deixe
  de corresponder a uma evidência esperada. Citações incorretas e falta de recuperação não mudam a verdade do texto;
  reprovam seus próprios critérios. Um trecho irrelevante pode ter groundedness 1 e resposta incorreta.
- `citation_correctness`: unidades com citação válida / unidades respondidas. Cada unidade exige uma citação;
  a identidade, fonte, ordinal, página e linhas devem coincidir com um trecho recuperado que sustente seu texto.
- `abstention_correct`: a presença da frase exata de abstention corresponde ao rótulo do cenário.
  Responder normalmente quando abstention não é esperada também conta como decisão correta.

Abstention reconhecida é exatamente “Não encontrei essa informação nos materiais da clínica.”.
Groundedness e citações ficam `null` sem afirmações; não recebem artificialmente nota 1.
Erros declarados e a mensagem padrão de indisponibilidade da busca produzem status `error` e todas as métricas `null`.
Uma resposta vazia é reprovada. O cenário só passa se todas as verificações aplicáveis forem satisfeitas.

As falhas de citação têm categorias distintas: `citation_missing`, `citation_malformed`, `citation_nonexistent`,
`citation_not_retrieved`, `citation_location_mismatch` e `citation_no_support`.
Uma citação passa pelas verificações nessa ordem; a primeira falha determina sua categoria.
O relatório também distingue `unsupported_claim`, `answer_incorrect` e `abstention_incorrect`.
Inclui os rótulos, observações, acervo completo, fingerprint, fornecedor, modelo, rubrica e limitações.

Não há julgamento por modelo nem promessa de entailment semântico geral. Paráfrases, resumos e outros formatos
precisam de revisão manual e de uma rubrica própria antes de interpretar os resultados como qualidade semântica.
Os fixtures demonstram o classificador; avaliar geração e recuperação reais exige capturas reais e rótulos revisados.
