package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/tools"
	"github.com/lpyaia/llm-clinic-scheduler/internal/knowledge"
)

// AnswerKnowledge runs the production extractive answer step over an explicit retrieval snapshot.
// It does not route tools or acquire conversation access. The caller owns retrieval and its lifetime.
// Empty retrieval abstains without a model call; cancellation never publishes an answer.
func AnswerKnowledge(ctx context.Context, model Model, question string, rows []knowledge.Result) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	if strings.TrimSpace(question) == "" || model == nil {
		return Result{}, contract.InvalidResponse()
	}

	outcome, err := tools.AnswerKnowledge(rows, toolSchemas.KnowledgeAnswer)
	if err != nil {
		return Result{}, err
	}

	if reply, ok := outcome.(tools.ReplyToPatient); ok {
		return reply.Result, nil
	}

	answer := outcome.(tools.ReturnToModel)

	arguments, err := json.Marshal(map[string]string{"question": question})
	if err != nil {
		return Result{}, err
	}

	request := Request{
		Messages: []Message{
			{Role: "system", Content: answer.Instructions},
			{Role: "user", Content: question},
			{
				Role:      "assistant",
				ToolCalls: []ToolCall{{ID: "knowledge", Name: "search_clinic_knowledge", Arguments: arguments}},
			},
			{Role: "tool", Content: string(answer.Content), ToolCallID: "knowledge"},
		},
		ResponseSchema: &answer.ResponseSchema,
	}

	response, err := model.Complete(ctx, request)
	if err != nil {
		return Result{}, err
	}

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	if len(response.ToolCalls) != 0 {
		return Result{}, contract.InvalidResponse()
	}

	return answer.Finalize(response.Content)
}
