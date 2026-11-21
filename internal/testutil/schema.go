//go:build integration

package testutil

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	_columnsQuery = `
select c.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
       pg_get_expr(d.adbin, d.adrelid), col_description(c.oid, a.attnum)
from pg_attribute a
join pg_class c on c.oid = a.attrelid
join pg_namespace n on n.oid = c.relnamespace
left join pg_attrdef d on d.adrelid = a.attrelid and d.adnum = a.attnum
where n.nspname = 'public' and c.relkind = 'r' and a.attnum > 0 and not a.attisdropped`
	_constraintsQuery = `
select c.relname, con.conname, pg_get_constraintdef(con.oid)
from pg_constraint con
join pg_class c on c.oid = con.conrelid
join pg_namespace n on n.oid = c.relnamespace
where n.nspname = 'public' and con.contype <> 'n'`
	_indexesQuery = `
select tablename, indexname, indexdef from pg_indexes
where schemaname = 'public'
  and indexname not in (select conname from pg_constraint)`
	_tableCommentsQuery = `
select c.relname, obj_description(c.oid, 'pg_class')
from pg_class c join pg_namespace n on n.oid = c.relnamespace
where n.nspname = 'public' and c.relkind = 'r'`
)

// pyRepr quotes s the way Python repr() quotes a str.
func pyRepr(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = `"`
	}

	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, quote, `\`+quote)

	return quote + escaped + quote
}

// SchemaSnapshot returns one line per column, constraint, index and table comment of the given tables, sorted.
func SchemaSnapshot(ctx context.Context, pool *pgxpool.Pool, tables []string) ([]string, error) {
	wanted := func(t string) bool { return slices.Contains(tables, t) }

	lines, err := columnLines(ctx, pool, wanted)
	if err != nil {
		return nil, err
	}

	for _, q := range []struct {
		sql, kind string
	}{{_constraintsQuery, "constraint"}, {_indexesQuery, "index"}} {
		more, err := triples(ctx, pool, q.sql, q.kind, wanted)
		if err != nil {
			return nil, err
		}

		lines = append(lines, more...)
	}

	comments, err := commentLines(ctx, pool, wanted)
	if err != nil {
		return nil, err
	}

	lines = append(lines, comments...)
	slices.Sort(lines)

	return lines, nil
}

func columnLine(table, column, kind string, notNull bool, def, comment *string) string {
	null := " NULL"
	if notNull {
		null = " NOT NULL"
	}

	line := "column " + table + "." + column + " " + kind + null

	if def != nil && *def != "" {
		line += " DEFAULT " + *def
	}

	if comment != nil && *comment != "" {
		line += " COMMENT " + pyRepr(*comment)
	}

	return line
}

func columnLines(ctx context.Context, pool *pgxpool.Pool, wanted func(string) bool) ([]string, error) {
	rows, err := pool.Query(ctx, _columnsQuery)
	if err != nil {
		return nil, fmt.Errorf("testutil: snapshot columns: %w", err)
	}
	defer rows.Close()

	var lines []string

	for rows.Next() {
		var (
			table, column, kind string
			notNull             bool
			def, comment        *string
		)

		if err := rows.Scan(&table, &column, &kind, &notNull, &def, &comment); err != nil {
			return nil, fmt.Errorf("testutil: snapshot columns: %w", err)
		}

		if wanted(table) {
			lines = append(lines, columnLine(table, column, kind, notNull, def, comment))
		}
	}

	return lines, nil
}

func commentLines(ctx context.Context, pool *pgxpool.Pool, wanted func(string) bool) ([]string, error) {
	rows, err := pool.Query(ctx, _tableCommentsQuery)
	if err != nil {
		return nil, fmt.Errorf("testutil: snapshot comments: %w", err)
	}
	defer rows.Close()

	var lines []string

	for rows.Next() {
		var (
			table   string
			comment *string
		)

		if err := rows.Scan(&table, &comment); err != nil {
			return nil, fmt.Errorf("testutil: snapshot comments: %w", err)
		}

		if wanted(table) && comment != nil && *comment != "" {
			lines = append(lines, "comment "+table+" "+pyRepr(*comment))
		}
	}

	return lines, nil
}

func triples(ctx context.Context, pool *pgxpool.Pool, sql, kind string, wanted func(string) bool) ([]string, error) {
	rows, err := pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("testutil: snapshot %s: %w", kind, err)
	}
	defer rows.Close()

	var lines []string

	for rows.Next() {
		var table, name, def string
		if err := rows.Scan(&table, &name, &def); err != nil {
			return nil, fmt.Errorf("testutil: snapshot %s: %w", kind, err)
		}

		if wanted(table) {
			lines = append(lines, kind+" "+table+"."+name+" "+def)
		}
	}

	return lines, nil
}
