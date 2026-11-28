// Package whatsapp adapts Meta Cloud API notifications to clinic conversations.
package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"github.com/valyala/fasthttp"
)

const (
	webhookPath     = "/webhooks/whatsapp"
	maxWebhookBytes = 3 * 1024 * 1024
)

var (
	phoneNumberIDPattern = regexp.MustCompile(`^[0-9]+$`)
	senderPhonePattern   = regexp.MustCompile(`^[1-9][0-9]{7,14}$`)
)

type MetaConfig struct {
	VerifyToken, AppSecret, AccessToken, PhoneNumberID string
}

type PatientLookup interface {
	PatientByPhone(context.Context, string) (domain.Patient, error)
}

type MetaWebhook struct {
	verifyTokenHash [sha256.Size]byte
	config          MetaConfig
	patients        PatientLookup
	assistant       *agent.Agent
	client          *http.Client
}

type notification struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata         struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
				Messages []incomingMessage `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type incomingMessage struct {
	From string `json:"from"`
	ID   string `json:"id"`
	Type string `json:"type"`
	Text struct {
		Body string `json:"body"`
	} `json:"text"`
}

func NewMetaWebhook(
	config MetaConfig, patients PatientLookup, assistant *agent.Agent, client *http.Client,
) (*MetaWebhook, error) {
	if strings.TrimSpace(config.VerifyToken) == "" || strings.TrimSpace(config.AppSecret) == "" ||
		strings.TrimSpace(config.AccessToken) == "" || !phoneNumberIDPattern.MatchString(config.PhoneNumberID) {
		return nil, errors.New("verifique as variáveis WHATSAPP_META_* do WhatsApp Cloud API")
	}

	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	return &MetaWebhook{
		verifyTokenHash: sha256.Sum256([]byte(config.VerifyToken)),
		config:          config,
		patients:        patients,
		assistant:       assistant,
		client:          client,
	}, nil
}

func (w *MetaWebhook) Register(app *fiber.App) {
	server := app.Server()
	previous := server.HeaderReceived
	server.HeaderReceived = func(header *fasthttp.RequestHeader) fasthttp.RequestConfig {
		var config fasthttp.RequestConfig
		if previous != nil {
			config = previous(header)
		}

		path, _, _ := bytes.Cut(header.RequestURI(), []byte("?"))
		if bytes.Equal(path, []byte(webhookPath)) && bytes.Equal(header.Method(), []byte(fiber.MethodPost)) {
			config.MaxRequestBodySize = maxWebhookBytes
		}

		return config
	}

	app.Get(webhookPath, w.verify)
	app.Post(webhookPath, w.receive)
}

func (w *MetaWebhook) verify(c fiber.Ctx) error {
	tokenHash := sha256.Sum256([]byte(c.Query("hub.verify_token")))
	challenge := c.Query("hub.challenge")

	if c.Query("hub.mode") != "subscribe" || challenge == "" ||
		subtle.ConstantTimeCompare(tokenHash[:], w.verifyTokenHash[:]) != 1 {
		return c.Status(fiber.StatusForbidden).SendString("")
	}

	return c.Type("txt").SendString(challenge)
}

func (w *MetaWebhook) receive(c fiber.Ctx) error {
	body := c.BodyRaw()
	if !w.authentic(body, c.Get("X-Hub-Signature-256")) {
		return c.Status(fiber.StatusForbidden).SendString("")
	}

	var event notification
	if err := json.Unmarshal(body, &event); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("")
	}

	if event.Object != "whatsapp_business_account" {
		return c.SendString("")
	}

	status := fiber.StatusOK

	for _, entry := range event.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" || change.Value.MessagingProduct != "whatsapp" ||
				change.Value.Metadata.PhoneNumberID != w.config.PhoneNumberID {
				continue
			}

			for _, message := range change.Value.Messages {
				if err := w.reply(c.Context(), message); err != nil {
					status = fiber.StatusServiceUnavailable
				}
			}
		}
	}

	return c.Status(status).SendString("")
}

func (w *MetaWebhook) authentic(body []byte, header string) bool {
	digest, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(digest) != sha256.Size*2 {
		return false
	}

	signature, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(w.config.AppSecret))
	_, _ = mac.Write(body)

	return hmac.Equal(mac.Sum(nil), signature)
}

func (w *MetaWebhook) reply(ctx context.Context, message incomingMessage) error {
	text := strings.TrimSpace(message.Text.Body)
	if message.Type != "text" || strings.TrimSpace(message.ID) == "" || !senderPhonePattern.MatchString(message.From) ||
		text == "" || len(text) > agent.MaxMessageBytes {
		return nil
	}

	patient, err := w.patients.PatientByPhone(ctx, "+"+message.From)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	conversationID := uuid.NewSHA1(patient.ID, []byte("whatsapp:meta:"+w.config.PhoneNumberID))
	principal := domain.Principal{PatientID: patient.ID}
	// Only a revision tied to a proposal the patient saw can grant confirmation authority.
	out, err := w.assistant.ReplyMessage(ctx, principal, agent.MessageInput{
		ID: uuid.NewSHA1(conversationID, []byte(message.ID)), ConversationID: conversationID,
		CreateConversation: true, Text: text,
	})
	if err != nil {
		var failure *agent.TurnError
		if errors.As(err, &failure) {
			if guidance, saved := failure.SavedFailureMessage(); saved {
				return w.sendText(ctx, message, guidance)
			}
		}

		return err
	}

	answer := out.Message
	if out.PendingAction != nil {
		answer = out.PendingAction.Summary + "\nPara concluir esta operação, fale com a equipe da clínica."
	}

	return w.sendText(ctx, message, answer)
}
