package trace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/internal/trace"
)

func TestExporterOmitsContentByDefaultAndRedactsSyntheticCredentials(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata", true: "synthetic"}[capture], func(t *testing.T) {
			var output bytes.Buffer

			apiKey := "sk-test-private-provider-key"
			sessionToken := strings.Repeat("a1", 32)
			recorder := trace.NewJSONL(&output, trace.Options{
				CaptureSyntheticContent: capture, Secrets: []string{"opaque-config-value", apiKey},
			})
			run := recorder.Start(context.Background(), "Paciente simulado Authorization: Bearer "+sessionToken)
			step := run.StartStep(trace.Step{
				Kind: "model", CallID: apiKey,
			}, map[string]any{
				"api_key": apiKey, "password": "unrecognized-password", "nested": []any{
					map[string]any{"Authorization": "Bearer arbitrary-session-value"},
				},
				"Content": `{"refresh_token":"arbitrary-refresh-value","message":"` + apiKey + `"}`,
				"message": "Paciente simulado opaque-config-value " + apiKey + " " + sessionToken,
			})
			step.End(map[string]string{"Cookie": "session=private-cookie"}, errors.New(apiKey))
			run.Finish(map[string]string{"message": "Bearer " + sessionToken}, errors.New(apiKey))

			for _, secret := range []string{
				apiKey, sessionToken, "opaque-config-value", "unrecognized-password", "arbitrary-session-value",
				"arbitrary-refresh-value", "private-cookie",
			} {
				if bytes.Contains(output.Bytes(), []byte(secret)) {
					t.Fatalf("credential %q appeared in exported output", secret)
				}
			}

			var execution trace.Execution
			if err := json.Unmarshal(output.Bytes(), &execution); err != nil {
				t.Fatal(err)
			}

			if capture && !bytes.Contains(output.Bytes(), []byte("Paciente simulado")) {
				t.Fatal("redaction removed harmless synthetic content")
			}

			if !capture && (execution.Input != nil || execution.Output != nil ||
				execution.Steps[0].Input != nil || execution.Steps[0].Output != nil) {
				t.Fatal("default exporter retained conversational content")
			}
		})
	}
}

func TestExporterRedactsSecretsThatAreJSONScalars(t *testing.T) {
	for _, secret := range []string{"123456789012345678", strings.Repeat("7", 64), "true", "null"} {
		t.Run(secret, func(t *testing.T) {
			var output bytes.Buffer

			recorder := trace.NewJSONL(&output, trace.Options{
				CaptureSyntheticContent: true, Secrets: []string{secret},
			})
			run := recorder.Start(context.Background(), secret)
			step := run.StartStep(trace.Step{Kind: "tool"}, json.RawMessage(`{"value":`+secret+`}`))
			step.End(json.RawMessage(secret), nil)
			run.Finish(map[string]string{"message": secret}, nil)

			if bytes.Contains(output.Bytes(), []byte(secret)) {
				t.Fatal("JSON scalar credential bypassed redaction")
			}

			if !json.Valid(output.Bytes()) || !bytes.Contains(output.Bytes(), []byte("[redacted]")) {
				t.Fatal("credential redaction must preserve valid JSON")
			}
		})
	}
}
