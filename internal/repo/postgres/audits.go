package postgres

import (
	"context"
	"encoding/json"
	"fmt"
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
