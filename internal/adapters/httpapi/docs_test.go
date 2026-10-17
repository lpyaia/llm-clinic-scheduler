package httpapi

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lpyaia/llm-clinic-scheduler/api"
	"github.com/lpyaia/llm-clinic-scheduler/internal/timeouts"
)

func TestDocumentationIsPublicAndUsesEmbeddedContract(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := New(nil, nil, logger, timeouts.DefaultHTTP)

	for _, test := range []struct {
		path        string
		contentType string
	}{
		{path: "/docs", contentType: "text/html"},
		{path: "/docs/", contentType: "text/html"},
		{path: "/openapi.yaml", contentType: "application/yaml"},
	} {
		t.Run(test.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", test.path, nil)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Error(err)
				}
			}()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}

			if resp.StatusCode != 200 {
				t.Fatalf("anonymous request returned %d: %s", resp.StatusCode, body)
			}

			if !strings.HasPrefix(resp.Header.Get("Content-Type"), test.contentType) {
				t.Fatalf("unexpected content type: %s", resp.Header.Get("Content-Type"))
			}

			if test.path == "/openapi.yaml" {
				if string(body) != api.OpenAPI() {
					t.Fatal("served specification differs from embedded contract")
				}

				return
			}

			for _, expected := range []string{"SwaggerUIBundle", `url: "/openapi.yaml"`} {
				if !strings.Contains(string(body), expected) {
					t.Fatalf("documentation is missing %q", expected)
				}
			}
		})
	}
}
