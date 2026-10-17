package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
)

type localKey int

const (
	principalKey localKey = iota
	requestIDKey
)

func errorStatus(err error) (int, string, string) {
	var business *domain.Error
	if errors.As(err, &business) {
		status := 409

		switch business.Code {
		case "INVALID_INPUT":
			status = 400
		case "UNAUTHORIZED":
			status = 401
		case "NOT_FOUND":
			status = 404
		case "ACTION_EXPIRED":
			status = 410
		}

		return status, business.Code, business.Message
	}

	var transport *fiber.Error
	if errors.As(err, &transport) {
		switch transport.Code {
		case 404:
			return 404, "NOT_FOUND", "Recurso não encontrado."
		case 405:
			return 405, "METHOD_NOT_ALLOWED", "Método não permitido."
		case 413:
			return 413, "INVALID_INPUT", "Requisição muito grande."
		}
	}

	return 500, "INTERNAL_ERROR", "Não foi possível concluir a operação. Tente novamente."
}

func New(service *application.Service, ping func(context.Context) error, logger *slog.Logger) *fiber.App {
	app := fiber.New(fiber.Config{Immutable: true, BodyLimit: 64 * 1024, ErrorHandler: func(c fiber.Ctx, err error) error {
		status, code, message := errorStatus(err)
		id, _ := c.Locals(requestIDKey).(string)

		return c.Status(status).JSON(errorDTO{Error: errorDetail{Code: code, Message: message, RequestID: id}})
	}})
	app.Use(func(c fiber.Ctx) error {
		id := uuid.NewString()
		c.Locals(requestIDKey, id)
		c.Set("X-Request-ID", id)

		ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
		defer cancel()

		c.SetContext(ctx)

		start := time.Now()

		err := c.Next()
		status, code := c.Response().StatusCode(), ""
		if err != nil {
			status, code, _ = errorStatus(err)
		}

		logger.Info("http_request", "request_id", id, "method", c.Method(), "route", c.Route().Path, "status", status, "duration_ms", time.Since(start).Milliseconds(), "error_code", code)

		return err
	})
	app.Use(recover.New())
	registerDocumentation(app)

	app.Get("/health/live", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok"}) })
	app.Get("/health/ready", func(c fiber.Ctx) error {
		if err := ping(c.Context()); err != nil {
			id, _ := c.Locals(requestIDKey).(string)
			return c.Status(503).JSON(errorDTO{Error: errorDetail{Code: "NOT_READY", Message: "Banco de dados indisponível.", RequestID: id}})
		}

		return c.JSON(fiber.Map{"status": "ok"})
	})
	app.Post("/v1/dev/sessions", func(c fiber.Ctx) error {
		var in sessionRequest
		if err := decode(c, &in); err != nil {
			return err
		}

		out, err := service.StartSession(c.Context(), in.FullName, in.Phone)
		if err != nil {
			return err
		}

		return c.Status(201).JSON(sessionDTO{Token: out.Token, ExpiresAt: out.ExpiresAt.In(service.Location), Patient: patientDTO{ID: out.Patient.ID, FullName: out.Patient.FullName, Phone: out.Patient.Phone}})
	})
	v1 := app.Group("/v1", func(c fiber.Ctx) error {
		parts := strings.Fields(c.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return domain.ErrUnauthorized
		}

		p, err := service.Authenticate(c.Context(), parts[1])
		if err != nil {
			return err
		}

		c.Locals(principalKey, p)

		return c.Next()
	})
	v1.Get("/specialties", func(c fiber.Ctx) error {
		rows, err := service.ListSpecialties(c.Context())
		if err != nil {
			return err
		}

		out := make([]specialtyDTO, 0, len(rows))
		for _, r := range rows {
			out = append(out, specialtyDTO{ID: r.ID, Name: r.Name})
		}

		return c.JSON(out)
	})
	v1.Get("/doctors", func(c fiber.Ctx) error {
		id, err := optionalID(c.Query("specialty_id"))
		if err != nil {
			return err
		}

		rows, err := service.ListDoctors(c.Context(), id)
		if err != nil {
			return err
		}

		out := make([]doctorDTO, 0, len(rows))
		for _, r := range rows {
			out = append(out, doctorDTO{ID: r.ID, Name: r.Name, SpecialtyID: r.SpecialtyID})
		}

		return c.JSON(out)
	})
	v1.Get("/slots", func(c fiber.Ctx) error {
		doctorID, err := optionalID(c.Query("doctor_id"))
		if err != nil {
			return err
		}

		specialtyID, err := optionalID(c.Query("specialty_id"))
		if err != nil {
			return err
		}

		from, err := queryTime(c, "from")
		if err != nil {
			return err
		}

		to, err := queryTime(c, "to")
		if err != nil {
			return err
		}

		found, err := service.FindSlots(c.Context(), application.SearchInput{DoctorID: doctorID, SpecialtyID: specialtyID, Period: c.Query("period"), From: from, To: to})
		if err != nil {
			return err
		}

		out := searchDTO{Slots: make([]slotDTO, 0, len(found.Slots)), SearchedFrom: found.From.In(service.Location), SearchedUntil: found.Until.In(service.Location), Expanded: found.Expanded}
		for _, slot := range found.Slots {
			out.Slots = append(out.Slots, slotDTO{ID: slot.ID, DoctorID: slot.DoctorID, DoctorName: slot.DoctorName, StartsAt: slot.StartsAt.In(service.Location), DurationMinutes: 30})
		}

		return c.JSON(out)
	})
	v1.Get("/appointments", func(c fiber.Ctx) error {
		from, err := queryTime(c, "from")
		if err != nil {
			return err
		}

		to, err := queryTime(c, "to")
		if err != nil {
			return err
		}

		rows, err := service.ListAppointments(c.Context(), principal(c), application.AppointmentFilter{Status: c.Query("status"), From: from, To: to})
		if err != nil {
			return err
		}

		out := make([]appointmentDTO, 0, len(rows))
		for _, a := range rows {
			out = append(out, appointmentResponse(a, service.Location))
		}

		return c.JSON(out)
	})
	v1.Post("/pending-actions", func(c fiber.Ctx) error {
		var in prepareRequest
		if err := decode(c, &in); err != nil {
			return err
		}

		slotID, err := optionalID(in.SlotID)
		if err != nil {
			return err
		}

		appointmentID, err := optionalID(in.AppointmentID)
		if err != nil {
			return err
		}

		a, err := service.Prepare(c.Context(), principal(c), application.PrepareInput{Operation: in.Operation, SlotID: slotID, AppointmentID: appointmentID, Category: in.Category})
		if err != nil {
			return err
		}

		return c.Status(201).JSON(actionResponse(a, service.Location))
	})
	v1.Get("/pending-actions/:id", func(c fiber.Ctx) error {
		id, err := requiredID(c.Params("id"))
		if err != nil {
			return err
		}

		a, err := service.GetAction(c.Context(), principal(c), id)
		if err != nil {
			return err
		}

		return c.JSON(actionResponse(a, service.Location))
	})
	v1.Post("/pending-actions/:id/confirm", func(c fiber.Ctx) error {
		id, err := requiredID(c.Params("id"))
		if err != nil {
			return err
		}

		if len(bytes.TrimSpace(c.Body())) > 0 {
			var empty struct{}
			if err := decode(c, &empty); err != nil {
				return err
			}
		}

		a, err := service.Confirm(c.Context(), principal(c), id)
		if err != nil {
			return err
		}

		return c.JSON(appointmentResponse(a, service.Location))
	})

	return app
}
func principal(c fiber.Ctx) domain.Principal { return c.Locals(principalKey).(domain.Principal) }

func decode(c fiber.Ctx, out any) error {
	if strings.ToLower(strings.TrimSpace(strings.Split(c.Get("Content-Type"), ";")[0])) != "application/json" {
		return domain.Failure("INVALID_INPUT", "Use Content-Type application/json.")
	}

	body := bytes.TrimSpace(c.Body())
	if len(body) == 0 || body[0] != '{' {
		return domain.Failure("INVALID_INPUT", "Informe um objeto JSON válido.")
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(out); err != nil {
		return domain.Failure("INVALID_INPUT", "JSON inválido ou campos desconhecidos.")
	}

	if err := decoder.Decode(new(any)); err != io.EOF {
		return domain.Failure("INVALID_INPUT", "Informe somente um objeto JSON.")
	}

	return nil
}

func optionalID(id string) (uuid.UUID, error) {
	if id == "" {
		return uuid.Nil, nil
	}

	parsed, err := uuid.Parse(id)
	if len(id) != 36 || err != nil || parsed == uuid.Nil {
		return uuid.Nil, domain.Failure("INVALID_INPUT", "Identificador inválido.")
	}

	return parsed, nil
}

func requiredID(raw string) (uuid.UUID, error) {
	id, err := optionalID(raw)
	if err != nil {
		return uuid.Nil, err
	}

	if id == uuid.Nil {
		return uuid.Nil, domain.Failure("INVALID_INPUT", "Identificador obrigatório.")
	}

	return id, nil
}

func queryTime(c fiber.Ctx, name string) (time.Time, error) {
	if c.Query(name) == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339, c.Query(name))
	if err != nil {
		return time.Time{}, domain.Failure("INVALID_INPUT", "Datas devem usar RFC 3339.")
	}

	return t, nil
}
