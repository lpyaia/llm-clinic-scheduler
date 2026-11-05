package tools

import (
	"context"
	"encoding/json"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
)

type proposalResponse struct {
	schema json.RawMessage
}

// ProposalResponse is interpretation only; the flow owns authority and execution.
type ProposalResponse struct {
	Decision string
}

func (ProposalResponse) toolOutcome() {}

func (p *proposalResponse) Definition() contract.Tool {
	return contract.Tool{
		Name: "respond_to_proposal",
		Description: "Interpreta a mensagem atual sobre a proposta vinculada: confirm para aceitação sem condições, " +
			"reject para recusa, clarify para perguntas ou ambiguidade. Alterações usam as tools de busca/preparação.",
		Parameters: p.schema,
	}
}

func (*proposalResponse) Execute(_ context.Context, _ Input, raw json.RawMessage) (Outcome, error) {
	var args struct {
		Decision string `json:"decision"`
	}
	if err := contract.DecodeObject(raw, &args, "decision"); err != nil {
		return nil, contract.InvalidResponse()
	}

	switch args.Decision {
	case "confirm", "reject", "clarify":
		return ProposalResponse{Decision: args.Decision}, nil
	default:
		return nil, contract.InvalidResponse()
	}
}
