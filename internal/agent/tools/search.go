package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"golang.org/x/text/unicode/norm"
)

type slotSearch struct {
	core   *application.Service
	schema Schemas
}

func (s *slotSearch) Definition() contract.Tool {
	return contract.Tool{
		Name: "find_available_slots",
		Description: "Busca horários reais por nome de médico/especialidade e atualiza preferências. " +
			"null ou campo ausente preserva; string vazia limpa. Não aceita IDs.",
		Parameters: s.schema.FindAvailableSlots,
	}
}

func (s *slotSearch) Execute(ctx context.Context, input Input, raw json.RawMessage) (Outcome, error) {
	preferences := input.Search

	next, question, err := preferences.refine(raw, s.core.Clock, s.core.Location)
	if err != nil {
		return nil, err
	}

	if question != "" {
		return searchClarification(preferences, question), nil
	}

	doctors, err := s.core.ListDoctors(ctx, uuid.Nil)
	if err != nil {
		return nil, err
	}

	specialties, err := s.core.ListSpecialties(ctx)
	if err != nil {
		return nil, err
	}

	doctorNames, specialtyNames := map[uuid.UUID]string{}, map[uuid.UUID]string{}
	for _, row := range doctors {
		doctorNames[row.ID] = row.Name
	}

	for _, row := range specialties {
		specialtyNames[row.ID] = row.Name
	}

	doctorID, question := resolveName(next.Doctor, "médico", doctorNames)
	if question != "" {
		return searchClarification(preferences, question), nil
	}

	specialtyID, question := resolveName(next.Specialty, "especialidade", specialtyNames)
	if question != "" {
		return searchClarification(preferences, question), nil
	}

	if doctorID != uuid.Nil && specialtyID != uuid.Nil {
		for _, doctor := range doctors {
			if doctor.ID == doctorID && doctor.SpecialtyID != specialtyID {
				return searchClarification(preferences,
					"O médico escolhido não atende a especialidade informada. Qual preferência deseja alterar?",
				), nil
			}
		}
	}

	found, err := s.core.FindSlots(
		ctx,
		application.SearchInput{
			DoctorID:    doctorID,
			SpecialtyID: specialtyID,
			Period:      next.Period,
			From:        next.From,
			To:          next.To,
		},
	)
	if err != nil {
		if domain.ErrorCode(err) == "INVALID_INPUT" {
			return searchClarification(preferences, "Informe um intervalo futuro de até oito semanas."), nil
		}

		return nil, err
	}

	availability := &contract.Availability{
		Slots:    make([]contract.Slot, 0, len(found.Slots)),
		From:     found.From,
		Until:    found.Until,
		Expanded: found.Expanded,
	}

	var message strings.Builder
	if len(found.Slots) == 0 {
		message.WriteString("Não encontrei horários disponíveis")
	} else {
		message.WriteString("Horários disponíveis")
	}

	fmt.Fprintf(
		&message,
		" no intervalo de %s até %s (fim exclusivo):",
		found.From.Format("02/01/2006 às 15:04 -07:00"),
		found.Until.Format("02/01/2006 às 15:04 -07:00"),
	)

	if found.Expanded {
		message.WriteString(" Busca ampliada automaticamente.")
	}

	for _, row := range found.Slots {
		slot := contract.Slot{ID: row.ID, DoctorName: row.DoctorName, StartsAt: row.StartsAt.In(s.core.Location)}
		availability.Slots = append(availability.Slots, slot)
		fmt.Fprintf(&message, "\n- %s com %s.", slot.StartsAt.Format("02/01/2006 às 15:04 -07:00"), slot.DoctorName)
	}

	return ReplyToPatient{Search: &preferences, Result: contract.Result{
		Message: message.String(), Appointments: []contract.Appointment{}, Availability: availability,
	}}, nil
}

func normalizeName(name string) string {
	var text strings.Builder

	for _, char := range norm.NFD.String(strings.ToLower(name)) {
		if unicode.Is(unicode.Mn, char) {
			continue
		}

		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			text.WriteRune(char)
		} else {
			text.WriteRune(' ')
		}
	}

	words := strings.Fields(text.String())
	if len(words) > 0 && (words[0] == "dr" || words[0] == "dra" || words[0] == "doutor" || words[0] == "doutora") {
		words = words[1:]
	}

	return strings.Join(words, " ")
}

func resolveName(query, kind string, catalog map[uuid.UUID]string) (uuid.UUID, string) {
	if query == "" {
		return uuid.Nil, ""
	}

	normalized := normalizeName(query)

	var (
		ids   []uuid.UUID
		names []string
	)

	for id, name := range catalog {
		candidate := normalizeName(name)
		if normalized != "" && strings.Contains(" "+candidate+" ", " "+normalized+" ") {
			ids, names = append(ids, id), append(names, name)
		}
	}

	if len(ids) == 1 {
		return ids[0], ""
	}

	if len(ids) == 0 {
		return uuid.Nil, fmt.Sprintf("Não encontrei esse nome no catálogo de %s. Qual nome você deseja procurar?", kind)
	}

	slices.Sort(names)

	return uuid.Nil, fmt.Sprintf(
		"Encontrei mais de uma opção de %s: %s. Qual você prefere?",
		kind,
		strings.Join(names, "; "),
	)
}
