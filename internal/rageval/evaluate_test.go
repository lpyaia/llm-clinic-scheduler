package rageval_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
	"github.com/lpyaia/llm-clinic-scheduler/internal/rageval"
)

const parkingID = "aa000000-0000-0000-0000-000000000001:1"
const parkingText = "Há estacionamento gratuito na entrada da clínica."
const parkingCitation = "Fonte: faq.md (documento aa000000-0000-0000-0000-000000000001, trecho 1, linhas 2–2)."

func parkingDataset() rageval.Dataset {
	return rageval.Dataset{
		Version: 1,
		Corpus: []knowledge.Result{{
			DocumentID: uuid.MustParse("aa000000-0000-0000-0000-000000000001"),
			Source:     "faq.md", Ordinal: 1, Text: parkingText, StartLine: 2, EndLine: 2,
		}},
		Scenarios: []rageval.Scenario{{
			ID: "parking", Question: "Posso estacionar?", ReferenceAnswer: "Sim, é gratuito.",
			ExpectedEvidence: []string{parkingID},
		}},
	}
}

func evaluateParking(t *testing.T, observation rageval.Observation) rageval.Report {
	t.Helper()

	dataset := parkingDataset()

	hash, err := dataset.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}

	report, err := rageval.Evaluate(dataset, rageval.Run{
		DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
		Responses: map[string]rageval.Observation{"parking": observation},
	})
	if err != nil {
		t.Fatal(err)
	}

	return report
}

func TestRetrievedAnswerHasIndependentCorrectnessAndCitationScores(t *testing.T) {
	report := evaluateParking(t, rageval.Observation{
		Retrieved: []string{parkingID},
		MessageLines: []string{
			"Encontrei nos materiais da clínica:", "", parkingText, parkingCitation,
		},
	})
	result := report.Scenarios[0]
	if report.Totals.Passed != 1 || result.Status != "passed" ||
		result.Groundedness == nil || *result.Groundedness != 1 ||
		result.AnswerCorrect == nil || !*result.AnswerCorrect ||
		result.CitationCorrectness == nil || *result.CitationCorrectness != 1 ||
		result.AbstentionCorrect == nil || !*result.AbstentionCorrect || len(result.UnsupportedClaims) != 0 {
		t.Fatalf("expected a grounded, correct answer with a valid citation: %+v", report)
	}
}

func TestPlausibleAnswerWithoutEvidenceIsUnsupported(t *testing.T) {
	report := evaluateParking(t, rageval.Observation{
		Retrieved: []string{parkingID}, MessageLines: []string{"O estacionamento custa R$ 20 por hora."},
	})
	result := report.Scenarios[0]
	if report.Totals.Failed != 1 || result.Status != "failed" || *result.Groundedness != 0 ||
		*result.AnswerCorrect || len(result.UnsupportedClaims) != 1 ||
		result.UnsupportedClaims[0] != "O estacionamento custa R$ 20 por hora." {
		t.Fatalf("a plausible claim must not count as supported or correct: %+v", result)
	}
}

func TestCitationFailuresRemainDistinctFromFactualCorrectness(t *testing.T) {
	for _, test := range []struct {
		name, citation, failure string
		retrieved               []string
		groundedness            float64
	}{
		{"invented", "Fonte: faq.md (documento aa000000-0000-0000-0000-000000000001, trecho 99, linhas 2–2).",
			"citation_nonexistent", []string{parkingID}, 1},
		{"not retrieved", parkingCitation, "citation_not_retrieved", []string{}, 0},
		{"wrong passage", "Fonte: faq.md (documento aa000000-0000-0000-0000-000000000001, trecho 2, linhas 4–4).",
			"citation_no_support", []string{parkingID, "aa000000-0000-0000-0000-000000000001:2"}, 1},
		{"wrong location", "Fonte: faq.md (documento aa000000-0000-0000-0000-000000000001, trecho 1, linhas 9–9).",
			"citation_location_mismatch", []string{parkingID}, 1},
		{"missing", "", "citation_missing", []string{parkingID}, 1},
		{"malformed", "Fonte: faq.md", "citation_malformed", []string{parkingID}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataset := parkingDataset()
			dataset.Corpus = append(dataset.Corpus, knowledge.Result{
				DocumentID: dataset.Corpus[0].DocumentID, Source: "faq.md", Ordinal: 2,
				Text: "A entrada tem rampa.", StartLine: 4, EndLine: 4,
			})
			hash, _ := dataset.Fingerprint()

			report, err := rageval.Evaluate(dataset, rageval.Run{
				DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
				Responses: map[string]rageval.Observation{"parking": {
					Retrieved: test.retrieved, MessageLines: []string{parkingText, test.citation},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}

			result := report.Scenarios[0]
			if result.Status != "failed" || !*result.AnswerCorrect ||
				*result.CitationCorrectness != 0 || *result.Groundedness != test.groundedness ||
				!slices.ContainsFunc(result.Failures, func(f rageval.Failure) bool { return f.Kind == test.failure }) {
				t.Fatalf("expected %s while preserving answer correctness: %+v", test.failure, result)
			}
		})
	}
}

func TestAbstentionIsScoredAgainstLabelsAndOperationalErrorsStayUnscored(t *testing.T) {
	for _, test := range []struct {
		name, message, operationalError, status string
		expectAbstention, correct               bool
	}{
		{"correct abstention", "Não encontrei essa informação nos materiais da clínica.", "", "passed", true, true},
		{"undue abstention", "Não encontrei essa informação nos materiais da clínica.", "", "failed", false, false},
		{"error", "", "retrieval failed", "error", true, false},
		{"unavailable", "Não consegui consultar os materiais da clínica agora. Tente novamente mais tarde.",
			"", "error", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataset := parkingDataset()
			dataset.Scenarios[0].AbstentionExpected = test.expectAbstention
			if test.expectAbstention {
				dataset.Scenarios[0].ExpectedEvidence = []string{}
			}

			hash, _ := dataset.Fingerprint()

			report, err := rageval.Evaluate(dataset, rageval.Run{
				DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
				Responses: map[string]rageval.Observation{"parking": {
					Retrieved: []string{parkingID}, MessageLines: []string{test.message}, Error: test.operationalError,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}

			result := report.Scenarios[0]
			if result.Status != test.status || result.Groundedness != nil || result.CitationCorrectness != nil {
				t.Fatalf("abstention and errors must not inflate factual scores: %+v", result)
			}

			if test.status == "error" {
				if report.Totals.Errors != 1 || result.AnswerCorrect != nil || result.AbstentionCorrect != nil {
					t.Fatalf("operational failure must stay unscored: %+v", result)
				}
			} else if *result.AnswerCorrect != test.correct || *result.AbstentionCorrect != test.correct {
				t.Fatalf("abstention classification differs from manual labels: %+v", result)
			}
		})
	}
}

func TestIdenticalPassagesInDifferentSourcesDoNotDistortAnswerCorrectness(t *testing.T) {
	dataset := parkingDataset()
	duplicate := dataset.Corpus[0]
	duplicate.DocumentID = uuid.MustParse("bb000000-0000-0000-0000-000000000001")
	duplicate.Source = "access.md"
	dataset.Corpus = append(dataset.Corpus, duplicate)
	hash, _ := dataset.Fingerprint()

	report, err := rageval.Evaluate(dataset, rageval.Run{
		DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
		Responses: map[string]rageval.Observation{"parking": {
			Retrieved: []string{parkingID}, MessageLines: []string{parkingText, parkingCitation},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if report.Totals.Passed != 1 || !*report.Scenarios[0].AnswerCorrect {
		t.Fatalf("another source containing the same fact must not become an extra claim: %+v", report)
	}
}

func TestPartialSupportScoresClaimsAndPreservesUncitedTrailingText(t *testing.T) {
	report := evaluateParking(t, rageval.Observation{
		Retrieved: []string{parkingID},
		MessageLines: []string{
			"Encontrei nos materiais da clínica:", "", parkingText, parkingCitation,
			"", "O estacionamento custa R$ 20 por hora.",
		},
	})
	result := report.Scenarios[0]
	if result.Status != "failed" || *result.Groundedness != 0.5 || *result.CitationCorrectness != 0.5 ||
		*result.AnswerCorrect || len(result.UnsupportedClaims) != 1 ||
		result.UnsupportedClaims[0] != "O estacionamento custa R$ 20 por hora." {
		t.Fatalf("trailing claims must remain visible and lower both ratios: %+v", result)
	}
}

func TestMultiParagraphExcerptsAreScoredAsWholePassages(t *testing.T) {
	dataset := parkingDataset()
	dataset.Corpus[0].Text = "Há estacionamento gratuito.\n\nA entrada fica na rua lateral."
	hash, _ := dataset.Fingerprint()

	report, err := rageval.Evaluate(dataset, rageval.Run{
		DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
		Responses: map[string]rageval.Observation{"parking": {
			Retrieved: []string{parkingID},
			MessageLines: []string{
				"Encontrei nos materiais da clínica:", "", "Há estacionamento gratuito.",
				"", "A entrada fica na rua lateral.", parkingCitation,
			},
		}},
	})
	if err != nil || report.Totals.Passed != 1 {
		t.Fatalf("paragraph breaks must not fragment one cited excerpt: %+v, %v", report, err)
	}
}

func TestAbstentionTextAppendedToAnAnswerIsNotDiscarded(t *testing.T) {
	report := evaluateParking(t, rageval.Observation{
		Retrieved: []string{parkingID},
		MessageLines: []string{
			parkingText, parkingCitation, "", "Não encontrei essa informação nos materiais da clínica.",
		},
	})
	if report.Totals.Failed != 1 || len(report.Scenarios[0].UnsupportedClaims) != 1 {
		t.Fatalf("additional contradictory text must not be silently discarded: %+v", report)
	}
}

func TestSourceLabelsWithinExcerptTextRemainCitedData(t *testing.T) {
	dataset := parkingDataset()
	dataset.Corpus[0].Text = "Há estacionamento gratuito.\nFonte: manual de acesso."
	hash, _ := dataset.Fingerprint()

	report, err := rageval.Evaluate(dataset, rageval.Run{
		DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
		Responses: map[string]rageval.Observation{"parking": {
			Retrieved: []string{parkingID},
			MessageLines: []string{
				"Há estacionamento gratuito.", "Fonte: manual de acesso.", parkingCitation,
			},
		}},
	})
	if err != nil || report.Totals.Passed != 1 {
		t.Fatalf("source labels inside a retrieved passage are data, not rendered citations: %+v, %v", report, err)
	}
}

func TestExactExtractsWithSourceWhitespaceRemainSupported(t *testing.T) {
	dataset := parkingDataset()
	dataset.Corpus[0].Text = "  Há estacionamento gratuito.  \nFonte: manual de acesso.  "
	hash, _ := dataset.Fingerprint()

	report, err := rageval.Evaluate(dataset, rageval.Run{
		DatasetSHA256: hash, Provider: "fixture", Model: "manual-v1",
		Responses: map[string]rageval.Observation{"parking": {
			Retrieved: []string{parkingID},
			MessageLines: []string{
				"Encontrei nos materiais da clínica:", "", "  Há estacionamento gratuito.  ",
				"Fonte: manual de acesso.  ", parkingCitation,
			},
		}},
	})
	if err != nil || report.Totals.Passed != 1 || len(report.Scenarios[0].UnsupportedClaims) != 0 {
		t.Fatalf("source whitespace must not make an exact extract unsupported: %+v, %v", report, err)
	}
}
