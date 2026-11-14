// Package db embeds the normalized nestjs schema snapshot.
package db

import _ "embed"

// NestjsSchema is nestjs-schema.sql: one line per column, constraint, index and table comment.
//
//go:embed nestjs-schema.sql
var NestjsSchema string
