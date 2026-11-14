package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

const _insertAuditSQL = `INSERT INTO "audit_logs"
("userId", "action", "entityType", "entityId", "oldValue", "newValue", "ipAddress", "userAgent", "requestId")
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

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
	err := a.db.dbtx(WithoutTx(ctx), func(db sqlcgen.DBTX) error {
		_, err := db.Exec(ctx, _insertAuditSQL, l.UserID, l.Action, l.EntityType, l.EntityID,
			l.OldValue, l.NewValue, l.IPAddress, l.UserAgent, l.RequestID)

		return err
	})
	if err != nil {
		return fmt.Errorf("postgres: insert audit log: %w", err)
	}

	return nil
}
