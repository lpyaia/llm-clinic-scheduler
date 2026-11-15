package evals

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

func check(turn TurnReport) []string {
	failures := []string{}
	if turn.ErrorCode != turn.Expected.ErrorCode {
		failures = append(failures, fmt.Sprintf("error code: want %q, got %q",
			turn.Expected.ErrorCode, turn.ErrorCode))
	}

	actual := []ToolCall{}
	for _, call := range turn.Calls {
		actual = append(actual, call.Response.ToolCalls...)
	}

	if len(actual) != len(turn.Expected.Tools) {
		failures = append(failures, fmt.Sprintf("tool count: want %d, got %d", len(turn.Expected.Tools), len(actual)))
	}

	for i, expected := range turn.Expected.Tools {
		if i >= len(actual) {
			break
		}

		if expected.Name != actual[i].Name || !matchesArguments(expected.Arguments, actual[i].Arguments) {
			failures = append(failures, fmt.Sprintf("tool %d: expected %s arguments %s; got %s arguments %s",
				i+1, expected.Name, expected.Arguments, actual[i].Name, actual[i].Arguments))
		}
	}

	for _, effect := range turn.Effects {
		if !slices.Contains(turn.Expected.AllowedEffects, effect) {
			failures = append(failures, "forbidden effect: "+effect)
		}
	}

	if turn.Expected.Appointments != nil && len(turn.Appointments) != *turn.Expected.Appointments {
		failures = append(failures, fmt.Sprintf("stored appointments: want %d, got %d",
			*turn.Expected.Appointments, len(turn.Appointments)))
	}

	response := turn.Expected.Response
	for _, text := range response.Contains {
		if !strings.Contains(turn.Result.Message, text) {
			failures = append(failures, "response missing: "+text)
		}
	}

	for _, text := range response.Absent {
		if strings.Contains(turn.Result.Message, text) {
			failures = append(failures, "response contains forbidden text: "+text)
		}
	}

	if response.PendingAction != nil && (turn.Result.PendingAction != nil) != *response.PendingAction {
		failures = append(failures, "response pending_action mismatch")
	}

	if response.Availability != nil && (turn.Result.Availability != nil) != *response.Availability {
		failures = append(failures, "response availability mismatch")
	}

	if response.Appointments != nil && len(turn.Result.Appointments) != *response.Appointments {
		failures = append(failures, "response appointment count mismatch")
	}

	return failures
}

func matchesArguments(expected, actual json.RawMessage) bool {
	var want, got map[string]any
	if json.Unmarshal(expected, &want) != nil || json.Unmarshal(actual, &got) != nil || want == nil || got == nil {
		return false
	}

	for key, value := range want {
		actualValue, present := got[key]
		if !present || !reflect.DeepEqual(value, actualValue) {
			return false
		}
	}

	return true
}

func effects(before, after []domain.Appointment) []string {
	out := []string{}

	for _, current := range after {
		index := slices.IndexFunc(before, func(old domain.Appointment) bool { return old.ID == current.ID })
		if index < 0 {
			out = append(out, domain.CreateAppointment)
			continue
		}

		old := before[index]
		if current.Status == domain.Cancelled && old.Status != domain.Cancelled {
			out = append(out, domain.CancelAppointment)
		} else if current != old {
			out = append(out, domain.RescheduleAppointment)
		}
	}

	return out
}
