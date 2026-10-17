// Package api embeds the REST contract distributed with the application.
package api

import _ "embed"

//go:embed openapi.yaml
var specification string

func OpenAPI() string {
	return specification
}
