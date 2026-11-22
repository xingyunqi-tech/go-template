// Package openapi embeds the bundled OpenAPI document served at /openapi.json.
package openapi

import _ "embed"

// Document is openapi.json, produced by make openapi from src/.
//
//go:embed openapi.json
var Document []byte
