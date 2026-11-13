//go:build contract

package contract_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

type validationCase struct {
	ID       string          `json:"id"`
	DTO      string          `json:"dto"`
	Payload  json.RawMessage `json:"payload"`
	Expected json.RawMessage `json:"expected"`
}

func runCase(t *testing.T, v *validatorx.Validator, c *validationCase) string {
	t.Helper()

	payload, err := validatorx.ParseJSON(c.Payload)
	require.NoError(t, err)

	result := validatorx.NewObject()

	value, err := v.Run(c.DTO, "body", payload)

	var verr *validatorx.ValidationError

	switch {
	case errors.As(err, &verr):
		result.Set("ok", false)
		result.Set("status", float64(verr.Status))
		result.Set("errors", verr.Errors)
	case errors.Is(err, validatorx.ErrUnknownDTO):
		require.NoError(t, err)
	case err != nil:
		result.Set("ok", false)
		result.Set("status", 500.0)
		result.Set("errors", nil)
	default:
		result.Set("ok", true)
		result.Set("value", value)
	}

	out, ok := validatorx.Stringify(result)
	require.True(t, ok)

	return out
}

func TestValidationCasesMatchNestjsValidationPipe(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("fixtures", "validation-cases.json"))
	require.NoError(t, err)

	var cases []validationCase
	require.NoError(t, json.Unmarshal(data, &cases))
	require.NotEmpty(t, cases)

	v, err := validatorx.New(openapi.Validation)
	require.NoError(t, err)

	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()

			expected, err := validatorx.ParseJSON(c.Expected)
			require.NoError(t, err)

			want, ok := validatorx.Stringify(expected)
			require.True(t, ok)
			require.Equal(t, want, runCase(t, v, &c))
		})
	}
}
