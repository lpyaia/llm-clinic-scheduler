package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"github.com/lpyaia/llm-clinic-scheduler/internal/timeouts"
)

type clock struct{}

func (clock) Now() time.Time { return time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC) }

type httpRepo struct {
	application.Repository
	failed bool
}

func (r httpRepo) Session(context.Context, string) (domain.Session, error) {
	if r.failed {
		return domain.Session{}, errors.New("private database diagnostic")
	}

	return domain.Session{PatientID: uuid.New(), ExpiresAt: clock{}.Now().Add(time.Hour)}, nil
}
func (httpRepo) Action(context.Context, uuid.UUID, uuid.UUID, bool) (domain.Action, error) {
	return domain.Action{}, domain.ErrNotFound
}
func TestHTTPValidationAndAuthorization(t *testing.T) {
	s := &application.Service{Repo: httpRepo{}, Clock: clock{}, Location: time.UTC}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := New(s, func(context.Context) error { return nil }, logger, timeouts.DefaultHTTP)

	for _, test := range []struct {
		name, method, path, body, token string
		status                          int
		code                            string
	}{
		{"live", "GET", "/health/live", "", "", 200, ""},
		{"ready", "GET", "/health/ready", "", "", 200, ""},
		{"no session", "GET", "/v1/appointments", "", "", 401, "UNAUTHORIZED"},
		{"bad auth", "GET", "/v1/appointments", "", "Basic abc", 401, "UNAUTHORIZED"},
		{"bad phone", "POST", "/v1/dev/sessions", `{"phone":"123"}`, "", 400, "INVALID_INPUT"},
		{"unknown field", "POST", "/v1/dev/sessions", `{"phone":"+5511999999999","patient_id":"forged"}`, "", 400, "INVALID_INPUT"},
		{"null", "POST", "/v1/dev/sessions", `null`, "", 400, "INVALID_INPUT"},
		{"trailing json", "POST", "/v1/dev/sessions", `{} {}`, "", 400, "INVALID_INPUT"},
		{"malformed", "POST", "/v1/dev/sessions", `{`, "", 400, "INVALID_INPUT"},
		{"date", "GET", "/v1/slots?from=yesterday", "", "valid", 400, "INVALID_INPUT"},
		{"doctor id", "GET", "/v1/doctors?specialty_id=abc", "", "valid", 400, "INVALID_INPUT"},
		{"zero specialty", "GET", "/v1/doctors?specialty_id=" + uuid.Nil.String(), "", "valid", 400, "INVALID_INPUT"},
		{"malformed doctor", "GET", "/v1/slots?doctor_id=invalid", "", "valid", 400, "INVALID_INPUT"},
		{"zero doctor", "GET", "/v1/slots?doctor_id=" + uuid.Nil.String(), "", "valid", 400, "INVALID_INPUT"},
		{"malformed specialty", "GET", "/v1/slots?doctor_id=" + uuid.NewString() + "&specialty_id=invalid", "", "valid", 400, "INVALID_INPUT"},
		{"missing filters", "GET", "/v1/slots", "", "valid", 400, "INVALID_INPUT"},
		{"status", "GET", "/v1/appointments?status=bad", "", "valid", 400, "INVALID_INPUT"},
		{"action id", "GET", "/v1/pending-actions/bad", "", "valid", 400, "INVALID_INPUT"},
		{"zero action", "GET", "/v1/pending-actions/" + uuid.Nil.String(), "", "valid", 400, "INVALID_INPUT"},
		{"zero confirmation", "POST", "/v1/pending-actions/" + uuid.Nil.String() + "/confirm", "", "valid", 400, "INVALID_INPUT"},
		{"malformed slot", "POST", "/v1/pending-actions", `{"operation":"create_appointment","slot_id":"invalid","category":"consultation"}`, "valid", 400, "INVALID_INPUT"},
		{"zero slot", "POST", "/v1/pending-actions", `{"operation":"create_appointment","slot_id":"00000000-0000-0000-0000-000000000000","category":"consultation"}`, "valid", 400, "INVALID_INPUT"},
		{"malformed appointment", "POST", "/v1/pending-actions", `{"operation":"cancel_appointment","appointment_id":"invalid"}`, "valid", 400, "INVALID_INPUT"},
		{"ownership", "GET", "/v1/pending-actions/" + uuid.NewString(), "", "valid", 404, "NOT_FOUND"},
		{"confirm rejects overrides", "POST", "/v1/pending-actions/" + uuid.NewString() + "/confirm", `{"slot_id":"other"}`, "valid", 400, "INVALID_INPUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")

			if test.token == "valid" {
				req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
			} else if test.token != "" {
				req.Header.Set("Authorization", test.token)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Error(err)
				}
			}()

			if resp.StatusCode != test.status {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}

			if resp.Header.Get("X-Request-ID") == "" {
				t.Fatal("missing request id")
			}

			if test.code != "" {
				var out errorDTO
				if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
					t.Fatal(err)
				}

				if out.Error.Code != test.code {
					t.Fatalf("%+v", out)
				}
			}
		})
	}
}
func TestHTTPDoesNotExposeInfrastructureErrors(t *testing.T) {
	s := &application.Service{Repo: httpRepo{failed: true}, Clock: clock{}, Location: time.UTC}
	app := New(s, func(context.Context) error { return errors.New("private diagnostic") }, slog.New(slog.NewTextHandler(io.Discard, nil)), timeouts.DefaultHTTP)

	for _, path := range []string{"/v1/appointments", "/health/ready"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))

		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}

		body, _ := io.ReadAll(resp.Body)
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode < 500 || strings.Contains(string(body), "private") {
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
	}
}
