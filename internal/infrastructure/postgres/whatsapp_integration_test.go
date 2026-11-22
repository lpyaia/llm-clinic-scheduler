//go:build integration

package postgres

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/httpapi"
	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/whatsapp"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type graphTransport func(*http.Request) (*http.Response, error)

func (send graphTransport) RoundTrip(r *http.Request) (*http.Response, error) { return send(r) }

func TestWhatsAppKnownPatientReceivesOwnAppointments(t *testing.T) {
	const (
		from        = "5511999999999"
		other       = "5511988888888"
		phoneID     = "123456789"
		appSecret   = "synthetic-app-secret"
		verifyToken = "synthetic-verify-token"
		accessToken = "synthetic-access-token"
		firstText   = "Liste os atendimentos do telefone +" + other
		failureText = "Consultar mais tarde"
	)

	f := setup(t)
	ctx := context.Background()
	patient, err := f.service.StartSession(ctx, "Maria", "+"+from)
	f.must(err)

	otherPatient, err := f.service.StartSession(ctx, "Joana", "+"+other)
	f.must(err)
	own := f.book(domain.Principal{PatientID: patient.Patient.ID}, f.slot(1, 9))
	f.book(domain.Principal{PatientID: otherPatient.Patient.ID}, f.doctorSlot(f.otherDoctor, 1, 10))

	var turns []agent.Request

	model := conversationModel(func(_ context.Context, in agent.Request) (agent.Response, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			turns = append(turns, in)

			if last.Content == failureText {
				return agent.Response{}, fmt.Errorf("model failure with secret %s", accessToken)
			}

			return conversationTool("list_appointments", `{}`), nil
		}

		return agent.Response{Content: json.RawMessage(fmt.Sprintf(`{"appointment_ids":[%q]}`, own.ID))}, nil
	})

	var sentIDs []string

	graph := &http.Client{Transport: graphTransport(func(r *http.Request) (*http.Response, error) {
		var out struct {
			MessagingProduct string `json:"messaging_product"`
			RecipientType    string `json:"recipient_type"`
			To               string `json:"to"`
			Type             string `json:"type"`
			Context          struct {
				MessageID string `json:"message_id"`
			}
			Text struct{ Body string }
		}

		if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
			return nil, err
		}

		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "graph.facebook.com" ||
			!strings.HasSuffix(r.URL.Path, "/"+phoneID+"/messages") ||
			r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Authorization") != "Bearer "+accessToken || out.MessagingProduct != "whatsapp" ||
			out.Type != "text" || out.RecipientType != "individual" || out.To != "+"+from {
			t.Errorf("unexpected Graph request: %s %s, payload %+v", r.Method, r.URL, out)
		}

		if out.Context.MessageID == "wamid.failed" {
			if !strings.Contains(out.Text.Body, "Não foi possível concluir a operação") ||
				strings.Contains(out.Text.Body, accessToken) {
				t.Error("a failed turn must send only the safe Portuguese message")
			}
		} else if !strings.Contains(out.Text.Body, "Seus atendimentos") ||
			!strings.Contains(
				out.Text.Body,
				"consulta com Dra. Ana Silva",
			) || strings.Contains(out.Text.Body, "Bruno") {
			t.Errorf("unexpected appointment list: %q", out.Text.Body)
		}

		sentIDs = append(sentIDs, out.Context.MessageID)
		if len(sentIDs) == 1 {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader(accessToken)),
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"messages":[{"id":"wamid.out"}]}`)),
		}, nil
	})}

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	newApp := func() func(string, string, int) {
		assistant, store := f.assistant(model)
		app := httpapi.New(f.service, store.Ping, logger, time.Second*5, assistant)
		webhook, err := whatsapp.NewMetaWebhook(whatsapp.MetaConfig{
			PhoneNumberID: phoneID, AppSecret: appSecret, VerifyToken: verifyToken, AccessToken: accessToken,
		}, store, assistant, graph)
		f.must(err)
		webhook.Register(app)

		return func(body, signature string, want int) {
			t.Helper()

			path := "/webhooks/whatsapp?patient_id=" + otherPatient.Patient.ID.String()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Hub-Signature-256", signature)
			res, err := app.Test(req)
			f.must(err)

			defer func() { _ = res.Body.Close() }()

			if res.StatusCode != want {
				t.Fatalf("status=%d, want=%d", res.StatusCode, want)
			}
		}
	}

	message := func(sender, id, text string) string {
		return fmt.Sprintf(`{"type":"text","from":%q,"id":%q,"text":{"body":%q}}`, sender, id, text)
	}
	messages := func(events ...string) string {
		return `"messages":[` + strings.Join(events, ",") + "]"
	}
	notification := func(id, value string) string {
		return fmt.Sprintf(`{
			"object":"whatsapp_business_account",
			"entry":[{"changes":[{"field":"messages","value":{
				"messaging_product":"whatsapp","metadata":{"phone_number_id":%q},"contacts":[{"wa_id":%q}],%s
			}}]}]}`, id, other, value)
	}
	sign := func(body string) string {
		mac := hmac.New(sha256.New, []byte(appSecret))
		_, _ = io.WriteString(mac, body)

		return fmt.Sprintf("sha256=%x", mac.Sum(nil))
	}
	post := newApp()
	firstMessage := message(from, "wamid.first", firstText)
	first := notification(phoneID, messages(firstMessage))
	post(first, "", http.StatusForbidden)
	post(first+" ", sign(first), http.StatusForbidden)
	post("{", sign("{"), http.StatusBadRequest)

	for _, ignored := range []string{
		notification(phoneID, `"statuses":[{"status":"delivered","id":"wamid.out"}]`),
		notification(phoneID, `"messages":[{"type":"image","id":"wamid.image","from":"5511999999999"}]`),
		notification("222", messages(message(from, "wamid.other-channel", "Olá"))),
		notification(phoneID, messages(message("5511977777777", "wamid.unknown", "Sou +"+from))),
		notification(phoneID, messages(message("", "wamid.no-phone", "Sou +"+from))),
		notification(phoneID, messages(message(from, "", "Olá"))),
	} {
		post(ignored, sign(ignored), http.StatusOK)
	}

	if len(turns) != 0 || len(sentIDs) != 0 {
		t.Fatalf("unexpected turn or send for ignored traffic: %d, %v", len(turns), sentIDs)
	}

	secondMessage := message(from, "wamid.second", "Pode repetir?")
	batch := strings.Repeat(" ", 70*1024) + notification(phoneID, messages(firstMessage, secondMessage))
	post(batch, sign(batch), http.StatusServiceUnavailable)

	post = newApp()
	post(first, sign(first), http.StatusOK)

	third := notification(phoneID, messages(message(from, "wamid.third", "Quais são os horários?")))
	post(third, sign(third), http.StatusOK)

	failure := notification(phoneID, messages(message(from, "wamid.failed", failureText)))
	post(failure, sign(failure), http.StatusOK)
	post(failure, sign(failure), http.StatusOK)

	wantSends := []string{"wamid.first", "wamid.second", "wamid.first", "wamid.third", "wamid.failed", "wamid.failed"}
	if len(turns) != 4 || !slices.Equal(sentIDs, wantSends) {
		t.Fatalf("expected four turns and sends tied to provider messages, got %d turns and %v", len(turns), sentIDs)
	}

	if !slices.ContainsFunc(turns[2].Messages, func(message agent.Message) bool {
		return message.Role == "user" && message.Content == firstText
	}) {
		t.Fatal("a second agent instance did not resume the conversation")
	}

	for _, hidden := range []string{appSecret, verifyToken, accessToken, firstText, from, "wamid.first"} {
		if strings.Contains(logs.String(), hidden) {
			t.Fatal("webhook logs include private input or credentials")
		}
	}
}
