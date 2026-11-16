package evals

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type Options struct {
	Mode          string
	Model         agent.Model
	Configuration Configuration
	Recording     *Report
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

// Run sequences scenarios and turns. Each scenario owns a fresh core, repository and conversation store.
func Run(ctx context.Context, dataset Dataset, options Options) (Report, error) {
	if err := dataset.validate(); err != nil {
		return Report{}, err
	}

	if options.Mode != "scripted" && options.Mode != "replay" && options.Mode != "real" {
		return Report{}, errors.New("mode must be scripted, replay or real")
	}

	if options.Mode == "real" && (options.Model == nil || options.Configuration.Provider == "" ||
		options.Configuration.Model == "") {
		return Report{}, errors.New("real mode requires a model and provider/model configuration")
	}

	encoded, err := json.Marshal(dataset)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		DatasetSHA256: fmt.Sprintf("%x", sha256.Sum256(encoded)),
		Configuration: Configuration{Mode: options.Mode, Provider: "simulated", Model: "scripted"},
		Reproduction:  "Deterministic simulation; generated identifiers are normalized.",
	}
	if options.Mode == "real" {
		report.Configuration = options.Configuration
		report.Configuration.Mode = options.Mode
		report.Reproduction = "Probabilistic generation is not exactly reproducible; replay uses recorded responses."
	}

	if options.Mode == "replay" {
		if options.Recording == nil || options.Recording.DatasetSHA256 != report.DatasetSHA256 ||
			len(options.Recording.Scenarios) != len(dataset.Scenarios) {
			return Report{}, errors.New("replay requires a report from the same dataset")
		}

		report.Configuration = Configuration{Mode: "replay", Provider: "recorded", Model: "recorded"}
		report.Source = &options.Recording.Configuration
		if options.Recording.Source != nil {
			report.Source = options.Recording.Source
		}
	}

	for i, scenario := range dataset.Scenarios {
		var recording *ScenarioReport
		if options.Mode == "replay" {
			recording = &options.Recording.Scenarios[i]
			if recording.ID != scenario.ID || len(recording.Turns) > len(scenario.Turns) {
				return Report{}, errors.New("recorded scenario ID or turn count does not match dataset")
			}
		}

		result := runScenario(ctx, scenario, options, recording)
		report.Scenarios = append(report.Scenarios, result)

		switch result.Status {
		case "passed":
			report.Totals.Passed++
		case "failed":
			report.Totals.Failed++
		case "error":
			report.Totals.Errors++
		}
	}

	return report, nil
}

func runScenario(ctx context.Context, scenario Scenario, options Options, recording *ScenarioReport) ScenarioReport {
	location, _ := time.LoadLocation(scenario.Timezone)
	controlledClock := &clock{now: scenario.Now}
	repo := newRepository(scenario.Initial)
	core := &application.Service{Repo: repo, Clock: controlledClock, Location: location}
	ids := newIdentifiers(scenario)
	model := &observedModel{ids: ids, delegate: options.Model, mode: options.Mode}
	assistant := agent.New(core, model)
	assistant.ClinicContact = scenario.ClinicContact
	principal := domain.Principal{PatientID: scenario.PatientID}
	id := uuid.Nil
	result := ScenarioReport{ID: scenario.ID, Status: "passed"}

	for i, turn := range scenario.Turns {
		advance, _ := time.ParseDuration(turn.Advance)
		controlledClock.now = controlledClock.now.Add(advance)

		model.responses, model.calls = turn.Responses, []Call{}
		model.replay = nil
		if recording != nil && i < len(recording.Turns) {
			model.replay = recording.Turns[i].Calls
		}

		before, beforeErr := core.ListAppointments(ctx, principal, application.AppointmentFilter{})

		out, err := assistant.ReplyConversation(ctx, principal, id, turn.Message)
		if out.ConversationID != uuid.Nil {
			id = out.ConversationID
		}

		var turnErr *agent.TurnError
		if errors.As(err, &turnErr) {
			id = turnErr.ConversationID
		}

		after, listErr := core.ListAppointments(ctx, principal, application.AppointmentFilter{})
		record := TurnReport{
			Message: turn.Message, Now: controlledClock.now, Expected: turn.Expect,
			Result: out, Appointments: after, Calls: model.calls,
			Effects: effects(before, after), Failures: []string{},
		}
		listErr = errors.Join(beforeErr, listErr)

		if err == nil && ((options.Mode == "scripted" && len(model.responses) > 0) ||
			(options.Mode == "replay" && len(model.replay) > 0)) {
			err = errors.New("unused recorded/scripted completions")
		}

		if err != nil {
			record.ErrorCode = domain.ErrorCode(err)
		}

		record.Failures = check(record)
		if listErr != nil || (err != nil && record.ErrorCode == "INTERNAL_ERROR") {
			record.Error = errors.Join(err, listErr).Error()
			result.Status = "error"
		} else if len(record.Failures) > 0 && result.Status != "error" {
			result.Status = "failed"
		}

		record, normalizeErr := normalized(ids, record)
		if normalizeErr != nil {
			record.Error = normalizeErr.Error()
			result.Status = "error"
		}

		result.Turns = append(result.Turns, record)
		if record.Error != "" || (err != nil && record.ErrorCode != turn.Expect.ErrorCode) {
			break
		}
	}

	return result
}

type observedModel struct {
	responses []Completion
	calls     []Call
	replay    []Call
	ids       *identifiers
	delegate  agent.Model
	mode      string
}

func (m *observedModel) Complete(ctx context.Context, request agent.Request) (agent.Response, error) {
	// Snapshot before the agent updates its prompt or appends tool results.
	snapshot, err := normalized(m.ids, request)
	if err != nil {
		return agent.Response{}, err
	}

	response := Completion{Error: "scripted model responses exhausted"}

	var originalErr error

	switch m.mode {
	case "scripted":
		if len(m.responses) > 0 {
			response, m.responses = m.responses[0], m.responses[1:]
		}
	case "real":
		out, modelErr := m.delegate.Complete(ctx, request)
		originalErr = modelErr

		response = Completion{Content: out.Content, Usage: out.Usage}
		for _, call := range out.ToolCalls {
			response.ToolCalls = append(
				response.ToolCalls,
				ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments},
			)
		}

		if modelErr != nil {
			response.Error, response.ErrorCode = modelErr.Error(), domain.ErrorCode(modelErr)
			if errors.Is(modelErr, context.Canceled) {
				response.ErrorKind = "canceled"
			} else if errors.Is(modelErr, context.DeadlineExceeded) {
				response.ErrorKind = "deadline_exceeded"
			}
		}
	case "replay":
		response = Completion{Error: "recorded model responses exhausted"}

		if len(m.replay) > 0 {
			recorded := m.replay[0]
			m.replay = m.replay[1:]

			want, _ := json.Marshal(recorded.Request)
			got, _ := json.Marshal(snapshot)
			if string(want) != string(got) {
				response.Error = "recorded request does not match current prompt, tools or configuration"
			} else {
				encoded, _ := json.Marshal(recorded.Response)
				response = Completion{}
				if err := json.Unmarshal(m.ids.restore(encoded), &response); err != nil {
					return agent.Response{}, err
				}
			}
		}
	}

	stableResponse, err := normalized(m.ids, response)
	if err != nil {
		return agent.Response{}, err
	}

	m.calls = append(m.calls, Call{Request: snapshot, Response: stableResponse})
	out := agent.Response{Content: response.Content, Usage: response.Usage}
	for _, call := range response.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, agent.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}

	if response.Error != "" {
		if originalErr != nil {
			return out, originalErr
		}

		switch response.ErrorKind {
		case "canceled":
			return out, context.Canceled
		case "deadline_exceeded":
			return out, context.DeadlineExceeded
		}

		if response.ErrorCode != "" {
			return out, domain.Failure(response.ErrorCode, response.Error)
		}

		return out, errors.New(response.Error)
	}

	return out, nil
}
