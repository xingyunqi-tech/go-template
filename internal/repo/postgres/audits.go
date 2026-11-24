package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Audits implements repo.Audits.
type Audits struct {
	db *DB
}

// NewAudits returns Audits.
func NewAudits(db *DB) *Audits {
	return &Audits{db: db}
}

var _ repo.Audits = (*Audits)(nil)

// Create implements repo.Audits.
func (a *Audits) Create(ctx context.Context, l *repo.AuditLog) error {
	var data []byte

	if l.Data != nil {
		var err error
		if data, err = json.Marshal(l.Data); err != nil {
			return fmt.Errorf("postgres: encode audit data: %w", err)
		}
	}

	err := a.db.queries(ctx, func(q *sqlcgen.Queries) error {
		return q.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
			ID: uuid.NewV7(), ActorSub: l.ActorSub, Action: l.Action, ResourceType: l.ResourceType, ResourceID: l.ResourceID,
			RequestID: l.RequestID, Ip: l.IP, Data: data,
		})
	})
	if err != nil {
		return fmt.Errorf("postgres: insert audit log: %w", err)
	}

	return nil
}

// Purge implements repo.Purger: the audit logs of the actor lose its sub and IP.
func (a *Audits) Purge(ctx context.Context, actor uuid.UUID) error {
	if err := a.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.AnonymizeActor(ctx, &actor) }); err != nil {
		return fmt.Errorf("postgres: anonymize audit logs: %w", err)
	}

	return nil
}

// List implements repo.Audits.
func (a *Audits) List(ctx context.Context, p *repo.AuditPage) ([]repo.AuditEntry, error) {
	var out []repo.AuditEntry

	err := a.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListAuditLogs(ctx, sqlcgen.ListAuditLogsParams{
			ActorSub: p.ActorSub, Action: p.Action, AfterCreatedAt: p.AfterCreatedAt, AfterID: p.AfterID,
			RowLimit: int32(min(p.Limit, _maxRows)), //nolint:gosec // bounded
		})
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		out = make([]repo.AuditEntry, 0, len(rows))

		for _, row := range rows {
			e := repo.AuditEntry{
				AuditLog: repo.AuditLog{
					ActorSub: row.ActorSub, Action: row.Action, ResourceType: row.ResourceType, ResourceID: row.ResourceID,
					RequestID: row.RequestID, IP: row.Ip, Data: nil,
				},
				ID: row.ID, CreatedAt: row.CreatedAt,
			}

			if row.Data != nil {
				if err := json.Unmarshal(row.Data, &e.Data); err != nil {
					return fmt.Errorf("decode audit data: %w", err)
				}
			}

			out = append(out, e)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list audit logs: %w", err)
	}

	return out, nil
}

// _pruneBatch is how many audit logs one DELETE removes.
const _pruneBatch = 10_000

// DeleteBefore implements repo.Audits in batches of _pruneBatch, each its own statement.
func (a *Audits) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	var total int64

	for {
		var n int64

		err := a.db.queries(ctx, func(q *sqlcgen.Queries) error {
			var err error

			n, err = q.DeleteAuditLogsBefore(ctx, sqlcgen.DeleteAuditLogsBeforeParams{Before: before, RowLimit: _pruneBatch})

			return err //nolint:wrapcheck // wrapped below
		})
		if err != nil {
			return total, fmt.Errorf("postgres: prune audit logs: %w", err)
		}

		total += n

		if n < _pruneBatch {
			return total, nil
		}
	}
}
