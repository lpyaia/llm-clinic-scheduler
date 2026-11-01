package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"golang.org/x/text/unicode/norm"
)

type Slot struct {
	ID         uuid.UUID `json:"id"`
	DoctorName string    `json:"doctor_name"`
	StartsAt   time.Time `json:"starts_at"`
}

type Availability struct {
	Slots    []Slot    `json:"slots"`
	From     time.Time `json:"searched_from"`
	Until    time.Time `json:"searched_until"`
	Expanded bool      `json:"expanded"`
}

func searchTool() Tool {
	return Tool{
		Name: "find_available_slots",
		Description: "Busca horários reais por nome de médico/especialidade e atualiza preferências. " +
			"null ou campo ausente preserva; string vazia limpa. Não aceita IDs.",
		Parameters: json.RawMessage(findAvailableSlotsSchema),
	}
}

func clarification(message string) Result {
	return Result{Message: message, Appointments: []Appointment{}}
}

func (a *Agent) search(ctx context.Context, raw json.RawMessage, preferences *SearchPreferences) (Result, error) {
	next, question, err := preferences.refine(raw, a.core.Clock, a.core.Location)
	if err != nil {
		return Result{}, err
	}

	if question != "" {
		return clarification(question), nil
	}

	doctors, err := a.core.ListDoctors(ctx, uuid.Nil)
	if err != nil {
		return Result{}, err
	}

	specialties, err := a.core.ListSpecialties(ctx)
	if err != nil {
		return Result{}, err
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
		return clarification(question), nil
	}

	specialtyID, question := resolveName(next.Specialty, "especialidade", specialtyNames)
	if question != "" {
		return clarification(question), nil
	}

	if doctorID != uuid.Nil && specialtyID != uuid.Nil {
		for _, doctor := range doctors {
			if doctor.ID == doctorID && doctor.SpecialtyID != specialtyID {
				return clarification(
					"O médico escolhido não atende a especialidade informada. Qual preferência deseja alterar?",
				), nil
			}
		}
	}

	found, err := a.core.FindSlots(
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
			return clarification("Informe um intervalo futuro de até oito semanas."), nil
		}

		return Result{}, err
	}

	availability := &Availability{
		Slots:    make([]Slot, 0, len(found.Slots)),
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
		slot := Slot{ID: row.ID, DoctorName: row.DoctorName, StartsAt: row.StartsAt.In(a.core.Location)}
		availability.Slots = append(availability.Slots, slot)
		fmt.Fprintf(&message, "\n- %s com %s.", slot.StartsAt.Format("02/01/2006 às 15:04 -07:00"), slot.DoctorName)
	}

	return Result{Message: message.String(), Appointments: []Appointment{}, Availability: availability}, nil
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
