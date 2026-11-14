// Package seeds embeds the seed data shared with nestjs-template.
package seeds

import _ "embed"

// Data is seed.json: roles, statuses, permissions, role permissions and config.
//
//go:embed seed.json
var Data []byte

// Regions is region-data.json: the province / city / district tree.
//
//go:embed region-data.json
var Regions []byte
