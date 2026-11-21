package app

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
)

func TestAccountCleanersRunInNestjsOrder(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)
	names := newAccountCleanup(postgres.NewDB(nil, 0, log), nil, nil, nil, log).CleanerNames()

	var positions []int
	for _, name := range []string{"users", "files"} {
		positions = append(positions, slices.Index(names, name))
	}

	assert.NotContains(t, positions, -1, names)
	assert.True(t, slices.IsSorted(positions), names)
}
