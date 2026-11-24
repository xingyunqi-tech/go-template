package optional_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/optional"
)

type input struct {
	Name optional.Field[string] `json:"name"`
}

func TestFieldTellsAbsentFromNull(t *testing.T) {
	t.Parallel()

	var absent, null, set input

	require.NoError(t, json.Unmarshal([]byte(`{}`), &absent))
	require.NoError(t, json.Unmarshal([]byte(`{"name":null}`), &null))
	require.NoError(t, json.Unmarshal([]byte(`{"name":"ada"}`), &set))

	assert.False(t, absent.Name.Set)
	assert.True(t, null.Name.Set)
	assert.Nil(t, null.Name.Value)
	assert.True(t, set.Name.Set)
	assert.Equal(t, "ada", *set.Name.Value)

	require.Error(t, json.Unmarshal([]byte(`{"name":1}`), &set))

	out, err := json.Marshal(set)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"ada"}`, string(out))
}
