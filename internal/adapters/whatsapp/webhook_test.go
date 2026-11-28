package whatsapp_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/whatsapp"
)

func TestMetaVerifiesWebhookSubscription(t *testing.T) {
	app := fiber.New()

	webhook, err := whatsapp.NewMetaWebhook(whatsapp.MetaConfig{
		VerifyToken: "example-verify-token", AppSecret: "example-app-secret",
		AccessToken: "example-access-token", PhoneNumberID: "123456789",
	}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	webhook.Register(app)

	for _, tt := range []struct {
		name, query string
		status      int
	}{
		{"verified", "hub.mode=subscribe&hub.challenge=123&hub.verify_token=example-verify-token", http.StatusOK},
		{"wrong token", "hub.mode=subscribe&hub.challenge=123&hub.verify_token=wrong-token", http.StatusForbidden},
		{"wrong mode", "hub.mode=unsubscribe&hub.challenge=123&hub.verify_token=example-verify-token", 403},
		{"missing challenge", "hub.mode=subscribe&hub.verify_token=example-verify-token", 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := app.Test(httptest.NewRequest(http.MethodGet, "/webhooks/whatsapp?"+tt.query, nil))
			if err != nil {
				t.Fatal(err)
			}

			defer func() { _ = res.Body.Close() }()

			body, err := io.ReadAll(res.Body)
			if err != nil || res.StatusCode != tt.status {
				t.Fatalf("status=%d, want=%d, body=%q, err=%v", res.StatusCode, tt.status, body, err)
			}

			if tt.status == http.StatusOK && (string(body) != "123" ||
				!strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain")) {
				t.Fatalf("expected the unquoted challenge as text/plain, got headers %v and body %q", res.Header, body)
			}
		})
	}
}
