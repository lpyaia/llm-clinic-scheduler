package httpapi

import (
	_ "embed"

	"github.com/gofiber/fiber/v3"
	"github.com/lpyaia/llm-clinic-scheduler/api"
)

//go:embed docs.html
var documentationHTML string

func registerDocumentation(app *fiber.App) {
	app.Get("/docs", func(c fiber.Ctx) error {
		c.Set("Cache-Control", "no-cache")

		return c.Type("html", "utf-8").SendString(documentationHTML)
	})
	app.Get("/openapi.yaml", func(c fiber.Ctx) error {
		c.Set("Content-Type", "application/yaml; charset=utf-8")
		c.Set("Cache-Control", "no-cache")

		return c.SendString(api.OpenAPI())
	})
}
