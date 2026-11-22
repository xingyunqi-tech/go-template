// Package seeds embeds the demo accounts created outside production.
package seeds

import _ "embed"

// Data is seed.json: the demo accounts and their roles.
//
//go:embed seed.json
var Data []byte
