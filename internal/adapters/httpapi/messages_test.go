package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
)

func TestHTTPIdentifiedMessagesReplayAndValidateTheirIdentity(t *testing.T) {
	core := &application.Service{Repo: conversationRepo{first: uuid.New()}, Clock: clock{}, Location: time.UTC}
	calls := 0
	a := agent.New(core, conversationalModel(func(_ context.Context, in agent.Request) (agent.Response, error) {
		calls++

		if in.Messages[len(in.Messages)-1].Content == "Falha" {
			return agent.Response{}, errors.New("private provider failure")
		}

		return agent.Response{ToolCalls: []agent.ToolCall{{ID: "search", Name: "find_available_slots",
			Arguments: []byte(`{}`)}}}, nil
	}))
	app := New(core, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second, a)
	id := uuid.New()
	body := fmt.Sprintf(`{"message_id":%q,"message":"Horários","conversation_revision":0}`, id)

	request := func(body string, status int) string {
		t.Helper()

		req := httptest.NewRequest("POST", "/v1/agent/messages", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		req.Header.Set("Content-Type", "application/json")

		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}

		defer func() { _ = resp.Body.Close() }()

		payload, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != status {
			t.Fatalf("status=%d, want=%d, body=%s, err=%v", resp.StatusCode, status, payload, err)
		}

		return string(payload)
	}
	first := request(body, 200)
	if replay := request(body, 200); replay != first || calls != 1 || !strings.Contains(replay, id.String()) {
		t.Fatalf("replay executed again or changed response: %s", replay)
	}

	request(`{"message":"Horários"}`, 400)
	request(`{"message_id":null,"message":"Horários"}`, 400)
	request(`{"message_id":"invalid","message":"Horários"}`, 400)
	request(fmt.Sprintf(`{"message_id":%q,"message":"Horários","conversation_revision":-1}`, uuid.New()), 400)
	request(fmt.Sprintf(`{"message_id":%q,"message":"Outro texto"}`, id), 400)

	failureID := uuid.New()

	failureBody := fmt.Sprintf(`{"message_id":%q,"message":"Falha"}`, failureID)

	var original errorDTO
	if err := json.Unmarshal([]byte(request(failureBody, 500)), &original); err != nil {
		t.Fatal(err)
	}

	var replayed errorDTO
	if err := json.Unmarshal([]byte(request(failureBody, 500)), &replayed); err != nil {
		t.Fatal(err)
	}

	if calls != 2 || original.Error.MessageID != failureID.String() || original.Error.ConversationID == "" ||
		replayed.Error.MessageID != original.Error.MessageID || replayed.Error.Code != original.Error.Code ||
		replayed.Error.Message != original.Error.Message || strings.Contains(replayed.Error.Message, "private") {
		t.Fatalf("failed-message replay lost identity or safe error: %+v %+v", original, replayed)
	}
}
