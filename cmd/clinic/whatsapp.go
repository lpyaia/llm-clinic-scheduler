package main

import (
	"errors"
	"os"

	"github.com/gofiber/fiber/v3"
	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/whatsapp"
	"github.com/lpyaia/llm-clinic-scheduler/internal/agent"
)

func registerWhatsApp(app *fiber.App, patients whatsapp.PatientLookup, assistant *agent.Agent) error {
	provider := os.Getenv("WHATSAPP_PROVIDER")
	if provider == "" {
		return nil
	}

	if provider != "meta" {
		return errors.New("WHATSAPP_PROVIDER deve ser meta ou vazio")
	}

	if assistant == nil {
		return errors.New("configure OPENAI_API_KEY para habilitar o agente no WhatsApp")
	}

	webhook, err := whatsapp.NewMetaWebhook(whatsapp.MetaConfig{
		VerifyToken:   os.Getenv("WHATSAPP_META_VERIFY_TOKEN"),
		AppSecret:     os.Getenv("WHATSAPP_META_APP_SECRET"),
		AccessToken:   os.Getenv("WHATSAPP_META_ACCESS_TOKEN"),
		PhoneNumberID: os.Getenv("WHATSAPP_META_PHONE_NUMBER_ID"),
	}, patients, assistant, nil)
	if err != nil {
		return err
	}

	webhook.Register(app)

	return nil
}
