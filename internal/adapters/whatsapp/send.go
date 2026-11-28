package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"
)

const (
	graphAPI     = "https://graph.facebook.com/v25.0/"
	maxTextBytes = 4096
)

type textReply struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Context          struct {
		MessageID string `json:"message_id"`
	} `json:"context"`
	Text struct {
		Body string `json:"body"`
	} `json:"text"`
}

func (w *MetaWebhook) sendText(ctx context.Context, message incomingMessage, text string) error {
	for len(text) > maxTextBytes {
		end := maxTextBytes
		for end > maxTextBytes-utf8.UTFMax && !utf8.RuneStart(text[end]) {
			end--
		}

		if err := w.sendTextPart(ctx, message, text[:end]); err != nil {
			return err
		}

		text = text[end:]
	}

	return w.sendTextPart(ctx, message, text)
}

func (w *MetaWebhook) sendTextPart(ctx context.Context, message incomingMessage, text string) error {
	reply := textReply{
		MessagingProduct: "whatsapp", RecipientType: "individual", Type: "text", To: "+" + message.From,
	}
	reply.Context.MessageID = message.ID
	reply.Text.Body = text

	body, err := json.Marshal(reply)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		graphAPI+w.config.PhoneNumberID+"/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+w.config.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := w.client.Do(req)
	if err != nil {
		return errors.New("não foi possível enviar a resposta pelo WhatsApp")
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("WhatsApp recusou o envio da resposta")
	}

	return nil
}
