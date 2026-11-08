package tools

import (
	"context"
	"encoding/json"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
)

type appointmentSelection struct {
	core   *application.Service
	schema json.RawMessage
}

func (s *appointmentSelection) Definition() contract.Tool {
	return contract.Tool{
		Name: "select_appointment",
		Description: "Identifica um agendamento próprio futuro para remarcar. " +
			"Filtra os dados originais e pede escolha se ambíguo. Não altera reservas.",
		Parameters: s.schema,
	}
}

func (s *appointmentSelection) Execute(ctx context.Context, input Input, raw json.RawMessage) (Outcome, error) {
	candidates, err := findAppointmentCandidates(ctx, s.core, input.Principal, raw)
	if err != nil {
		return nil, err
	}

	message := RenderAppointments(candidates.Rows) + "\nQual agendamento você deseja remarcar?"
	preferences := SearchPreferences{}

	var selected *contract.Rescheduling

	if original := candidates.Selected; original != nil {
		selected = &contract.Rescheduling{
			Appointment: candidates.Rows[0], DoctorID: original.DoctorID, SlotID: original.SlotID,
		}
		preferences.criteria.Doctor = original.DoctorName
		message = RenderAppointments(candidates.Rows) + "\nPara qual novo horário deseja remarcar com o mesmo médico?"
	}

	return ReplyToPatient{
		Result: contract.Result{Message: message, Appointments: candidates.Rows}, Search: &preferences,
		Rescheduling: &ReschedulingUpdate{Selected: selected},
	}, nil
}
