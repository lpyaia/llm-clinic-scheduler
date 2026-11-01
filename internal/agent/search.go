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

// SearchPreferences stores resolved calendar boundaries, so follow-up messages
// retain the original dates even when the clock advances.
type SearchPreferences struct {
	Doctor, Specialty, Period string
	From, To                  time.Time
	// WindowFrom retains the selected lower bound before a weekday refinement.
	WindowFrom time.Time
}

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
	return Tool{Name: "find_available_slots", Description: "Busca horários reais por nome de médico/especialidade e atualiza preferências. null ou campo ausente preserva; string vazia limpa. Não aceita IDs.", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"doctor":{"type":["string","null"]},"specialty":{"type":["string","null"]},"period":{"type":["string","null"],"enum":["morning","afternoon","evening","",null]},"date":{"type":["string","null"],"enum":["today","tomorrow","this_week","next_week","earliest","after_monday","after_tuesday","after_wednesday","after_thursday","after_friday","after_saturday","after_sunday","clarify","",null]},"from":{"type":["string","null"],"description":"Início inclusivo RFC3339. String vazia limpa."},"to":{"type":["string","null"],"description":"Fim exclusivo RFC3339. String vazia limpa."}},"required":["doctor","specialty","period","date","from","to"]}`)}
}

func clarification(message string) Result {
	return Result{Message: message, Appointments: []Appointment{}}
}

func (a *Agent) search(ctx context.Context, raw json.RawMessage, preferences *SearchPreferences) (Result, error) {
	var args struct {
		Doctor, Specialty, Period, Date, From, To *string
	}
	if err := decodeObject(raw, &args, "doctor", "specialty", "period", "date", "from", "to"); err != nil {
		return Result{}, invalidResponse()
	}

	next := *preferences
	for _, field := range []struct {
		input  *string
		target *string
	}{{args.Doctor, &next.Doctor}, {args.Specialty, &next.Specialty}, {args.Period, &next.Period}} {
		if field.input != nil {
			*field.target = strings.TrimSpace(*field.input)
		}
	}

	if _, _, err := domain.PeriodBounds(next.Period); err != nil {
		return Result{}, invalidResponse()
	}

	if args.Date != nil && *args.Date == "clarify" {
		*preferences = next
		return clarification("Qual data ou intervalo você prefere? Informe também o período, se desejar."), nil
	}

	if err := a.searchDates(&next, args.Date, args.From, args.To); err != nil {
		return Result{}, err
	}

	// Save unresolved names as well: a clarification must retain the other preferences.
	*preferences = next
	if next.Doctor == "" && next.Specialty == "" {
		return clarification("Com qual médico ou especialidade você quer procurar horários?"), nil
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
				return clarification("O médico escolhido não atende a especialidade informada. Qual preferência deseja alterar?"), nil
			}
		}
	}

	found, err := a.core.FindSlots(ctx, application.SearchInput{DoctorID: doctorID, SpecialtyID: specialtyID, Period: next.Period, From: next.From, To: next.To})
	if err != nil {
		if domain.ErrorCode(err) == "INVALID_INPUT" {
			return clarification("Informe um intervalo futuro de até oito semanas."), nil
		}

		return Result{}, err
	}

	availability := &Availability{Slots: make([]Slot, 0, len(found.Slots)), From: found.From, Until: found.Until, Expanded: found.Expanded}

	var message strings.Builder
	if len(found.Slots) == 0 {
		message.WriteString("Não encontrei horários disponíveis")
	} else {
		message.WriteString("Horários disponíveis")
	}

	fmt.Fprintf(&message, " no intervalo de %s até %s (fim exclusivo):", found.From.Format("02/01/2006 às 15:04 -07:00"), found.Until.Format("02/01/2006 às 15:04 -07:00"))

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

func (a *Agent) searchDates(next *SearchPreferences, date, from, to *string) error {
	if date != nil {
		now := a.core.Clock.Now().In(a.core.Location)
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.core.Location)
		weekdayRefinement := false

		switch *date {
		case "", "earliest":
			next.From, next.To = time.Time{}, time.Time{}
		case "today":
			next.From, next.To = day, day.AddDate(0, 0, 1)
		case "tomorrow":
			next.From, next.To = day.AddDate(0, 0, 1), day.AddDate(0, 0, 2)
		case "this_week":
			next.From, next.To = domain.WeekBounds(now, a.core.Location)
		case "next_week":
			_, start := domain.WeekBounds(now, a.core.Location)
			next.From, next.To = start, start.AddDate(0, 0, 7)
		default:
			weekdayRefinement = true
			weekdays := map[string]time.Weekday{"after_monday": time.Monday, "after_tuesday": time.Tuesday, "after_wednesday": time.Wednesday, "after_thursday": time.Thursday, "after_friday": time.Friday, "after_saturday": time.Saturday, "after_sunday": time.Sunday}

			weekday, ok := weekdays[*date]
			if !ok {
				return invalidResponse()
			}

			base := next.WindowFrom
			if base.IsZero() {
				base, _ = domain.WeekBounds(now, a.core.Location)
				candidate := base.AddDate(0, 0, (int(weekday)+6)%7+1)
				if candidate.Before(day) {
					base = base.AddDate(0, 0, 7)
				}

				next.WindowFrom = base
			}

			start, _ := domain.WeekBounds(base, a.core.Location)
			start = start.AddDate(0, 0, (int(weekday)+6)%7+1)
			if start.Before(base) {
				start = base
			}

			next.From = start
		}

		if !weekdayRefinement {
			next.WindowFrom = next.From
		}
	}

	for _, field := range []struct {
		input  *string
		target *time.Time
	}{{from, &next.From}, {to, &next.To}} {
		if field.input == nil {
			continue
		}

		if *field.input == "" {
			*field.target = time.Time{}
			if field.target == &next.From {
				next.WindowFrom = time.Time{}
			}

			continue
		}

		parsed, err := time.Parse(time.RFC3339, *field.input)
		if err != nil {
			return invalidResponse()
		}

		*field.target = parsed.In(a.core.Location)
		if field.target == &next.From {
			next.WindowFrom = next.From
		}
	}

	return nil
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

	return uuid.Nil, fmt.Sprintf("Encontrei mais de uma opção de %s: %s. Qual você prefere?", kind, strings.Join(names, "; "))
}
