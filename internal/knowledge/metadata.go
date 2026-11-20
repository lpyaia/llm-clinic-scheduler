package knowledge

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var ErrAmbiguous = errors.New("specify the exam, procedure or doctor before retrieving this material")

// Metadata describes one subject and its applicable entities. Use a separate source for each context.
type Metadata struct {
	Topic     string `json:"topic"`
	Exam      string `json:"exam"`
	Procedure string `json:"procedure"`
	Doctor    string `json:"doctor"`
}

// Filter narrows retrieval before similarity ranking. Empty fields leave that dimension unrestricted.
type Filter Metadata

// ResolvedFilter tells persistence exactly which predicates to apply, without interpreting subject rules.
type ResolvedFilter struct {
	Filter
	GeneralOnly bool
}

func (f Filter) generalPolicy() bool {
	return f.Topic == "policy" && f.Exam == "" && f.Procedure == "" && f.Doctor == ""
}

// Clarification contains only catalog metadata, never document instructions or retrieved preparations.
type Clarification struct {
	Candidates []Metadata
}

func (*Clarification) Error() string { return ErrAmbiguous.Error() }

func (*Clarification) Is(target error) bool { return target == ErrAmbiguous }

func (m Metadata) Validate() error {
	if !slices.Contains(
		[]string{"", "general", "faq", "policy", "preparation", "insurance", "doctor_profile"},
		m.Topic,
	) {
		return ErrInput
	}

	for _, value := range []string{m.Exam, m.Procedure, m.Doctor} {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 200 || strings.ContainsAny(value, "\x00\r\n") {
			return ErrInput
		}
	}

	return nil
}

func entityKey(value string) string {
	var key strings.Builder

	for _, char := range norm.NFD.String(strings.ToLower(value)) {
		if unicode.Is(unicode.Mn, char) {
			continue
		}

		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			key.WriteRune(char)
		} else {
			key.WriteByte(' ')
		}
	}

	return strings.Join(strings.Fields(key.String()), " ")
}

func (s *Service) resolveFilter(ctx context.Context, filter Filter) (Filter, bool, error) {
	if filter == (Filter{}) {
		return filter, true, nil
	}

	scopes, err := s.repo.Scopes(ctx)
	if err != nil {
		return Filter{}, false, err
	}

	scopes = slices.DeleteFunc(scopes, func(scope Metadata) bool {
		return filter.Topic != "" && scope.Topic != filter.Topic
	})

	// Resolve supplied names against the corpus, preferring exact names to partial matches.
	for index, value := range []*string{&filter.Exam, &filter.Procedure, &filter.Doctor} {
		if *value == "" {
			continue
		}

		key := entityKey(*value)
		exact := slices.ContainsFunc(scopes, func(scope Metadata) bool {
			return key != "" && entityKey(scope.entity(index)) == key
		})
		scopes = slices.DeleteFunc(scopes, func(scope Metadata) bool {
			candidate := entityKey(scope.entity(index))
			partial := strings.Contains(" "+candidate+" ", " "+key+" ")

			return key == "" || (exact && candidate != key) || (!exact && !partial)
		})
	}

	if len(scopes) == 0 {
		return filter, false, nil
	}

	contextual := filter.Topic == "preparation" || filter.Topic == "doctor_profile" || filter.Topic == "policy"
	needsEntity := contextual && filter.Exam == "" && filter.Procedure == "" && filter.Doctor == ""
	if filter.generalPolicy() && slices.Contains(scopes, Metadata{Topic: "policy"}) {
		return filter, true, nil
	}

	if needsEntity && slices.ContainsFunc(scopes, func(scope Metadata) bool {
		return scope.Exam != "" || scope.Procedure != "" || scope.Doctor != ""
	}) {
		return Filter{}, false, &Clarification{Candidates: scopes}
	}

	if contextual && len(scopes) > 1 {
		return Filter{}, false, &Clarification{Candidates: scopes}
	}

	for index, value := range []*string{&filter.Exam, &filter.Procedure, &filter.Doctor} {
		if *value == "" {
			continue
		}

		resolved := scopes[0].entity(index)
		if slices.ContainsFunc(scopes, func(scope Metadata) bool { return scope.entity(index) != resolved }) {
			return Filter{}, false, &Clarification{Candidates: scopes}
		}

		*value = resolved
	}

	return filter, true, nil
}

func (m Metadata) entity(index int) string {
	return []string{m.Exam, m.Procedure, m.Doctor}[index]
}
