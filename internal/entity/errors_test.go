package entity_test

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

var _conventionsRow = regexp.MustCompile("^\\| `([a-z]+\\.[A-Za-z]+)` \\| (\\d{3}) \\| (\\d{5}) \\|")

func conventionsCodes(t *testing.T) map[string]entity.Code {
	t.Helper()

	doc, err := os.ReadFile("../../docs/platform-conventions.md")
	require.NoError(t, err)

	codes := map[string]entity.Code{}

	for _, line := range regexp.MustCompile("\r?\n").Split(string(doc), -1) {
		m := _conventionsRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		status, _ := strconv.Atoi(m[2])
		code, _ := strconv.Atoi(m[3])
		codes[m[1]] = entity.Code{Status: status, Code: code}
	}

	require.NotEmpty(t, codes)

	return codes
}

func TestCodesMatchTheConventions(t *testing.T) {
	t.Parallel()

	assert.Equal(t, conventionsCodes(t), entity.Codes())
}

func TestCodesAreStatusTimesHundredPlusSequence(t *testing.T) {
	t.Parallel()

	seen := map[int]string{}

	for key, c := range entity.Codes() {
		assert.Equal(t, c.Status, c.Code/100, key)

		if other, dup := seen[c.Code]; dup {
			t.Errorf("%s and %s share code %d", key, other, c.Code)
		}

		seen[c.Code] = key
	}
}

func TestErrorMatchesByKey(t *testing.T) {
	t.Parallel()

	cause := errors.New("redis down")
	err := entity.Wrap(entity.KeyServiceUnavailable, cause).WithData(map[string]any{"x": 1})

	assert.ErrorIs(t, err, entity.New(entity.KeyServiceUnavailable))
	assert.NotErrorIs(t, err, entity.New(entity.KeyNotFound))
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, "common.serviceUnavailable: redis down", err.Error())
	assert.Equal(t, map[string]any{"x": 1}, err.Data)
}

func TestValidationErrorListsFields(t *testing.T) {
	t.Parallel()

	err := &entity.ValidationError{Errors: []entity.FieldError{
		{Field: "email", Rule: entity.RuleEmail, Params: nil},
		{Field: "query.limit", Rule: entity.RuleMax, Params: map[string]any{"max": 100}},
	}}

	assert.Equal(t, "common.validationFailed: email: email, query.limit: max", err.Error())
	assert.Len(t, entity.Invalid("roles", entity.RuleContains, map[string]any{"value": "user"}).Errors, 1)
}
