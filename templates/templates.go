// Package templates embeds the nestjs mail templates, copied byte for byte, and the module
// generator templates.
package templates

import "embed"

// Mail holds mail/<name>.hbs.
//
//go:embed mail/*.hbs
var Mail embed.FS

// Module holds module/*.tmpl, the files of `make module`.
//
//go:embed module/*.tmpl
var Module embed.FS
