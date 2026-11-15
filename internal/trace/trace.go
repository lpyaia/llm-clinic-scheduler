// Package trace records provider-independent agent executions as local JSON lines.
package trace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent/contract"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type requestIDKey struct{}

// WithRequestID correlates an execution with the server-generated HTTP request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

type Options struct {
	// CaptureSyntheticContent must only be enabled for local, simulated patient data.
	// The default records metadata without conversational content.
	CaptureSyntheticContent bool
	// Secrets supplies configured credentials that must be removed even in free text.
	Secrets []string
	Pricing *Pricing
	// Now supplies a monotonic clock for durations; defaults to time.Now.
	Now func() time.Time
}

type Execution struct {
	TraceID        string          `json:"trace_id"`
	RequestID      string          `json:"request_id"`
	ConversationID uuid.UUID       `json:"conversation_id"`
	StartedAt      time.Time       `json:"started_at"`
	DurationNS     int64           `json:"duration_ns"`
	Outcome        string          `json:"outcome"`
	ErrorCode      string          `json:"error_code,omitempty"`
	FailureStepID  string          `json:"failure_step_id,omitempty"`
	Input          json.RawMessage `json:"input,omitempty"`
	Output         json.RawMessage `json:"output,omitempty"`
	Steps          []Step          `json:"steps"`
	Metrics        Metrics         `json:"metrics"`
}

type Step struct {
	ID         string               `json:"id"`
	ParentID   string               `json:"parent_id,omitempty"`
	Order      int                  `json:"order"`
	Kind       string               `json:"kind"`
	ToolName   string               `json:"tool_name,omitempty"`
	CallID     string               `json:"call_id,omitempty"`
	StartedAt  time.Time            `json:"started_at"`
	DurationNS int64                `json:"duration_ns"`
	Outcome    string               `json:"outcome"`
	ErrorCode  string               `json:"error_code,omitempty"`
	Input      json.RawMessage      `json:"input,omitempty"`
	Output     json.RawMessage      `json:"output,omitempty"`
	Model      *contract.ModelUsage `json:"model,omitempty"`
}

// JSONL serializes complete executions atomically within this process. Export failures
// are counted instead of changing use-case results. A writer must return promptly;
// export happens after conversation access has been released, even on cancellation.
type JSONL struct {
	mu       sync.Mutex
	writer   io.Writer
	options  Options
	redactor *redactor
	failures atomic.Uint64
}

func NewJSONL(writer io.Writer, options Options) *JSONL {
	if options.Now == nil {
		options.Now = time.Now
	}

	return &JSONL{writer: writer, options: options, redactor: newRedactor(options.Secrets)}
}

func (j *JSONL) Failures() uint64 { return j.failures.Load() }

// Run belongs to one sequential turn. A nil recorder disables tracing.
type Run struct {
	recorder *JSONL
	data     Execution
}

func (j *JSONL) Start(ctx context.Context, input string) *Run {
	if j == nil {
		return nil
	}

	id, _ := ctx.Value(requestIDKey{}).(string)
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		id = uuid.NewString()
	}

	return &Run{recorder: j, data: Execution{
		TraceID: uuid.NewString(), RequestID: id, StartedAt: j.options.Now(),
		Input: j.content(input), Steps: []Step{},
	}}
}

func (r *Run) SetConversationID(id uuid.UUID) {
	if r != nil {
		r.data.ConversationID = id
	}
}

type Span struct {
	run   *Run
	index int
}

// StartStep and End surround a single synchronous operation in the turn owner.
// They never acquire conversation access, save state, or change operation results.
func (r *Run) StartStep(step Step, input any) *Span {
	if r == nil {
		return nil
	}

	step.ID, step.Order, step.StartedAt = uuid.NewString(), len(r.data.Steps)+1, r.recorder.options.Now()
	step.ToolName = r.recorder.redactor.text(step.ToolName)
	if step.CallID != "" {
		// A provider-controlled identifier is correlation data, never trusted text.
		step.CallID = fmt.Sprintf("%x", sha256.Sum256([]byte(step.CallID)))
	}

	step.Input = r.recorder.content(input)
	r.data.Steps = append(r.data.Steps, step)

	return &Span{run: r, index: len(r.data.Steps) - 1}
}

func (s *Span) ID() string {
	if s == nil {
		return ""
	}

	return s.run.data.Steps[s.index].ID
}

func (s *Span) End(output any, err error) {
	if s == nil {
		return
	}

	step := &s.run.data.Steps[s.index]
	step.DurationNS = s.run.recorder.options.Now().Sub(step.StartedAt).Nanoseconds()

	step.Outcome, step.ErrorCode = outcome(err)
	step.Output = s.run.recorder.content(output)
	if err != nil && s.run.data.FailureStepID == "" {
		s.run.data.FailureStepID = step.ID
	}
}

func (r *Run) Finish(output any, err error) {
	if r == nil {
		return
	}

	// Unwinding can leave nested operations open. Close the innermost first so the
	// failure link points to the originating operation, without capturing panic text.
	for i := len(r.data.Steps) - 1; i >= 0; i-- {
		if r.data.Steps[i].Outcome == "" {
			if err == nil {
				err = errors.New("unfinished trace operation")
			}

			(&Span{run: r, index: i}).End(nil, err)
		}
	}

	r.data.DurationNS = r.recorder.options.Now().Sub(r.data.StartedAt).Nanoseconds()
	r.data.Outcome, r.data.ErrorCode = outcome(err)
	r.data.Output = r.recorder.content(output)
	r.data.Metrics = r.metrics()

	j := r.recorder
	j.mu.Lock()
	defer j.mu.Unlock()

	if err := json.NewEncoder(j.writer).Encode(r.data); err != nil {
		j.failures.Add(1)
	}
}

func (j *JSONL) content(value any) json.RawMessage {
	if !j.options.CaptureSyntheticContent || value == nil {
		return nil
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`"[unavailable]"`)
	}

	return j.redactor.content(raw)
}

func outcome(err error) (string, string) {
	if err == nil {
		return "ok", ""
	}

	if errors.Is(err, context.Canceled) {
		return "cancelled", "CANCELLED"
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "DEADLINE_EXCEEDED"
	}

	code := domain.ErrorCode(err)
	if code == "AGENT_STEP_LIMIT" {
		return "limit", code
	}

	switch code {
	case "INVALID_INPUT", "UNAUTHORIZED", "NOT_FOUND", "AGENT_UNAVAILABLE", "MODEL_INVALID_RESPONSE",
		"SLOT_UNAVAILABLE", "WEEKLY_LIMIT_REACHED", "ACTION_EXPIRED", "APPOINTMENT_CHANGED",
		"APPOINTMENT_NOT_MUTABLE", "ACTION_NOT_COMPLETED":
		return "error", code
	default:
		// Unknown errors and provider-defined codes may contain credentials.
		return "error", "INTERNAL_ERROR"
	}
}
