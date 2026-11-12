// Package openapi embeds the contract artifacts shared with nestjs-template.
package openapi

import _ "embed"

// Baseline is nestjs-baseline.json, served byte for byte at /docs-json.
//
//go:embed nestjs-baseline.json
var Baseline []byte
