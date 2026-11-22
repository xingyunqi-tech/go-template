// Package migrations embeds the goose SQL migrations: core holds the tables of every
// deployment, identity the tables of the issuing service.
package migrations

import (
	"embed"
	"io/fs"
)

var (
	//go:embed core/*.sql
	_core embed.FS
	//go:embed identity/*.sql
	_identity embed.FS
)

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

// Identity returns the migrations of the issuing service.
func Identity() Set {
	sub, _ := fs.Sub(_identity, "identity")

	return Set{Name: "identity", Table: "goose_identity_version", FS: sub}
}
