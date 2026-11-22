// Package migrations embeds the goose SQL migrations: core holds the tables of every
// deployment, identity the tables of the issuing service.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed core/*.sql
var _core embed.FS

// Set is one goose migration directory with its version table.
type Set struct {
	Name  string
	Table string
	FS    fs.FS
}

// Core returns the migrations every role applies.
func Core() Set {
	sub, _ := fs.Sub(_core, "core")

	return Set{Name: "core", Table: "goose_core_version", FS: sub}
}
