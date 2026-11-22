package seeds_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/seeds"
)

func TestDemoAccountsAllHaveTheUserRole(t *testing.T) {
	t.Parallel()

	var data struct {
		Accounts []struct {
			Email    string   `json:"email"`
			Password string   `json:"password"`
			Roles    []string `json:"roles"`
		} `json:"accounts"`
	}

	require.NoError(t, json.Unmarshal(seeds.Data, &data))
	require.Len(t, data.Accounts, 2)

	for _, a := range data.Accounts {
		assert.Contains(t, a.Roles, "user", a.Email)
		assert.GreaterOrEqual(t, len([]rune(a.Password)), 8, a.Email)
	}
}
