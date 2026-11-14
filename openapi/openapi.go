// Package openapi embeds the contract artifacts shared with nestjs-template.
package openapi

import _ "embed"

// Baseline is nestjs-baseline.json, served byte for byte at /docs-json.
//
//go:embed nestjs-baseline.json
var Baseline []byte

// Validation is validation.json exported by nestjs scripts/export-validation.ts.
//
//go:embed validation.json
var Validation []byte

// NestHandlers is nest_handlers.json: operationId → nestjs "Controller-handler", used in rate limit keys.
//
//go:embed nest_handlers.json
var NestHandlers []byte
