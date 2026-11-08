package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
)

type humanContact struct {
	schema json.RawMessage
}

func (h *humanContact) Definition() contract.Tool {
	return contract.Tool{
		Name: "offer_human_contact",
		Description: "Recusa aconselhamento médico e oferece contato humano para decisões clínicas, " +
			"pedidos fora do escopo ou não compreendidos. Não realiza operações de agendamento.",
		Parameters: h.schema,
	}
}

func (*humanContact) Execute(_ context.Context, input Input, raw json.RawMessage) (Outcome, error) {
	var args struct {
		Reason string `json:"reason"`
	}
	if err := contract.DecodeObject(raw, &args, "reason"); err != nil {
		return nil, contract.InvalidResponse()
	}

	var message string

	switch args.Reason {
	case "medical_advice":
		message = "Não posso fornecer diagnóstico, interpretar sintomas, indicar tratamentos ou medicamentos, " +
			"nem recomendar especialidades com base em sintomas. "
	case "out_of_scope":
		message = "Não consigo resolver essa solicitação pelo assistente de agendamentos. "
	case "not_understood":
		message = "Não consegui entender sua solicitação com segurança. "
	default:
		return nil, contract.InvalidResponse()
	}

	contact := strings.TrimSpace(input.ClinicContact)
	if contact == "" {
		message += "Entre em contato com a equipe da clínica. " +
			"O canal de contato não está configurado neste assistente."
	} else {
		message += "Entre em contato com a equipe da clínica: " + contact + "."
	}

	return ReplyToPatient{Result: Clarification(message)}, nil
}
