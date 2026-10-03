package api

import _ "embed"

// OpenAPISpec contains the raw OpenAPI 3.0 YAML specification.
//
//go:embed openapi.yaml
var OpenAPISpec []byte

// SwaggerJSON contains the raw OpenAPI specification in JSON format.
//
//go:embed swagger.json
var SwaggerJSON []byte

// SwaggerUIHTML contains the embedded interactive Swagger UI HTML page.
//
//go:embed swagger-ui/index.html
var SwaggerUIHTML []byte
