// Package templates embeds the nestjs mail templates, copied byte for byte.
package templates

import "embed"

// Mail holds mail/<name>.hbs.
//
//go:embed mail/*.hbs
var Mail embed.FS
