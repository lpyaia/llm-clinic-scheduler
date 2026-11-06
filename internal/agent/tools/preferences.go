package tools

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

// SearchPreferences owns preference updates and retains the selected interval
// separately from weekday refinements. Its zero value has no preferences.
type SearchPreferences struct {
	criteria   searchCriteria
	windowFrom time.Time
}

type searchCriteria struct {
	Doctor, Specialty, Period string
	From, To                  time.Time
}

// MarshalJSON preserves the preference context supplied to the model without
// exposing calendar bookkeeping to callers of the preference update operation.
func (p SearchPreferences) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		searchCriteria
		WindowFrom time.Time
	}{p.criteria, p.windowFrom})
}

// refine applies one tool call atomically. Clarifications retain valid updates;
// malformed arguments leave preferences unchanged. The conversational flow owns rollback
// if a later catalog or availability lookup fails.
func (p *SearchPreferences) refine(
	raw json.RawMessage,
	clock application.Clock,
	location *time.Location,
) (searchCriteria, string, error) {
	var args struct {
		Doctor, Specialty, Period, Date, From, To *string
		ClarificationQuestion                     *string `json:"clarification_question"`
	}
	if err := contract.DecodeObject(
		raw, &args, "doctor", "specialty", "period", "date", "from", "to", "clarification_question",
	); err != nil {
		return searchCriteria{}, "", contract.InvalidResponse()
	}

	clarifying := args.Date != nil && *args.Date == "clarify"
	question := ""
	if args.ClarificationQuestion != nil {
		question = strings.TrimSpace(*args.ClarificationQuestion)
	}

	if (clarifying && question == "") || (!clarifying && args.ClarificationQuestion != nil) {
		return searchCriteria{}, "", contract.InvalidResponse()
	}

	next := *p
	for _, field := range []struct {
		input  *string
		target *string
	}{
		{args.Doctor, &next.criteria.Doctor},
		{args.Specialty, &next.criteria.Specialty},
		{args.Period, &next.criteria.Period},
	} {
		if field.input != nil {
			*field.target = strings.TrimSpace(*field.input)
		}
	}

	if _, _, err := domain.PeriodBounds(next.criteria.Period); err != nil {
		return searchCriteria{}, "", contract.InvalidResponse()
	}

	if !clarifying {
		if err := next.resolveDates(args.Date, args.From, args.To, clock, location); err != nil {
			return searchCriteria{}, "", err
		}

		if next.criteria.Doctor == "" && next.criteria.Specialty == "" {
			question = "Com qual médico ou especialidade você quer procurar horários?"
		}
	}

	*p = next

	return next.criteria, question, nil
}

func (p *SearchPreferences) resolveDates(
	date, from, to *string,
	clock application.Clock,
	location *time.Location,
) error {
	if date != nil {
		now := clock.Now().In(location)
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
		weekdayRefinement := false

		switch *date {
		case "", "earliest":
			p.criteria.From, p.criteria.To = time.Time{}, time.Time{}
		case "today":
			p.criteria.From, p.criteria.To = day, day.AddDate(0, 0, 1)
		case "tomorrow":
			p.criteria.From, p.criteria.To = day.AddDate(0, 0, 1), day.AddDate(0, 0, 2)
		case "this_week":
			p.criteria.From, p.criteria.To = domain.WeekBounds(now, location)
		case "next_week":
			_, start := domain.WeekBounds(now, location)

			p.criteria.From, p.criteria.To = start, start.AddDate(0, 0, 7)
		default:
			weekdayRefinement = true
			weekdays := map[string]time.Weekday{
				"after_monday":    time.Monday,
				"after_tuesday":   time.Tuesday,
				"after_wednesday": time.Wednesday,
				"after_thursday":  time.Thursday,
				"after_friday":    time.Friday,
				"after_saturday":  time.Saturday,
				"after_sunday":    time.Sunday,
			}

			weekday, ok := weekdays[*date]
			if !ok {
				return contract.InvalidResponse()
			}

			base := p.windowFrom
			if base.IsZero() {
				base, _ = domain.WeekBounds(now, location)
				candidate := base.AddDate(0, 0, (int(weekday)+6)%7+1)
				if candidate.Before(day) {
					base = base.AddDate(0, 0, 7)
				}

				p.windowFrom = base
			}

			start, _ := domain.WeekBounds(base, location)
			start = start.AddDate(0, 0, (int(weekday)+6)%7+1)
			if start.Before(base) {
				start = base
			}

			p.criteria.From = start
		}

		if !weekdayRefinement {
			p.windowFrom = p.criteria.From
		}
	}

	for _, field := range []struct {
		input  *string
		target *time.Time
	}{{from, &p.criteria.From}, {to, &p.criteria.To}} {
		if field.input == nil {
			continue
		}

		if *field.input == "" {
			*field.target = time.Time{}
			if field.target == &p.criteria.From {
				p.windowFrom = time.Time{}
			}

			continue
		}

		parsed, err := time.Parse(time.RFC3339, *field.input)
		if err != nil {
			return contract.InvalidResponse()
		}

		*field.target = parsed.In(location)
		if field.target == &p.criteria.From {
			p.windowFrom = p.criteria.From
		}
	}

	return nil
}
