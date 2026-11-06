package tools_test

import (
	"errors"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func TestSearchClarificationRequiresSpecificQuestion(t *testing.T) {
	for _, test := range []struct {
		name, args, question string
	}{
		{
			name: "ask only about the ambiguous hour",
			args: `{
				"doctor":"Ana",
				"date":"clarify",
				"clarification_question":"  Você prefere às 10h ou às 22h na quarta-feira?  "
			}`,
			question: "Você prefere às 10h ou às 22h na quarta-feira?",
		},
		{name: "missing question", args: `{"date":"clarify"}`},
		{name: "null question", args: `{"date":"clarify","clarification_question":null}`},
		{name: "empty question", args: `{"date":"clarify","clarification_question":"  "}`},
		{name: "invalid question type", args: `{"date":"clarify","clarification_question":42}`},
		{name: "question without clarification", args: `{"clarification_question":"Qual horário?"}`},
		{
			name: "question with resolved date",
			args: `{"date":"tomorrow","clarification_question":"Qual horário?"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, repo := searchFixture()
			repo.doctorsErr = errors.New("clarification must not query the catalog")

			out, err := searchResult(t, core, test.args)
			if test.question == "" {
				if domain.ErrorCode(err) != "MODEL_INVALID_RESPONSE" {
					t.Fatalf("expected invalid clarification, got result=%+v err=%v", out, err)
				}

				return
			}

			if err != nil || out.Message != test.question || out.Availability != nil || out.Appointments == nil {
				t.Fatalf("unexpected clarification: result=%+v err=%v", out, err)
			}
		})
	}
}
