package app

import (
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
)

// corePurgers remove the data of a deleted user from the core tables, in order; the audit logs
// are anonymized last.
func corePurgers(db *postgres.DB, files *file.Service) []repo.Purger {
	return []repo.Purger{
		files,
		postgres.NewNotes(db),
		// modulegen:purgers
		postgres.NewAudits(db),
	}
}
