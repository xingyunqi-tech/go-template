package validatorx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const _pipe = `"pipe":{"whitelist":true,"forbidNonWhitelisted":false,"forbidUnknownValues":false,"transform":true,"errorHttpStatusCode":422}`

func TestNewRejectsUnsupportedMetadata(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"constraint": `{"type":"customValidation","key":"isFancy","constraints":[],"message":null,"defaultMessage":"x","each":false}`,
		"anonymous":  `{"type":"customValidation","key":"customValidation","constraints":[],"message":null,"defaultMessage":"unknown","each":false}`,
		"condition":  `{"type":"conditionalValidation","key":"conditionalValidation","constraints":[{"function":null,"source":"() => true"}],"message":null,"defaultMessage":null,"each":false}`,
	}

	for name, rule := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw := `{` + _pipe + `,"classes":{"D":{"source":null,"extends":null,"initialValues":{},"properties":{"a":{"designType":"String","transforms":[],"type":null,"validations":[` + rule + `]}}}}}`
			_, err := New([]byte(raw))
			require.ErrorIs(t, err, ErrUnsupported)
		})
	}

	raw := `{` + _pipe + `,"classes":{"D":{"source":null,"extends":null,"initialValues":{},"properties":{"a":{"designType":"String","transforms":[{"function":null,"source":"({ value }) => value"}],"type":null,"validations":[]}}}}}`
	_, err := New([]byte(raw))
	require.ErrorIs(t, err, ErrUnsupported)
}

func TestRunReportsUnknownDTO(t *testing.T) {
	t.Parallel()

	v, err := New([]byte(`{` + _pipe + `,"classes":{}}`))
	require.NoError(t, err)
	require.False(t, v.Has("Missing"))

	_, err = v.Run("Missing", "body", NewObject())
	require.ErrorIs(t, err, ErrUnknownDTO)
}

func TestRunRejectsPrimitiveBodyForDTOWithRules(t *testing.T) {
	t.Parallel()

	raw := `{` + _pipe + `,"classes":{"D":{"source":null,"extends":null,"initialValues":{},"properties":{"a":{"designType":"String","transforms":[],"type":null,"validations":[{"type":"customValidation","key":"isOptional","constraints":[],"message":null,"defaultMessage":null,"each":false}]}}}}}`
	_, err := New([]byte(raw))
	require.ErrorIs(t, err, ErrUnsupported)

	raw = `{` + _pipe + `,"classes":{"D":{"source":null,"extends":null,"initialValues":{},"properties":{"a":{"designType":"String","transforms":[],"type":null,"validations":[{"type":"conditionalValidation","key":"conditionalValidation","constraints":[{"function":null,"source":"(_, value) => value !== undefined"}],"message":null,"defaultMessage":null,"each":false}]}}}}}`
	v, err := New([]byte(raw))
	require.NoError(t, err)

	_, err = v.Run("D", "query", "x")

	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	require.Equal(t, 422, verr.Status)

	s, _ := Stringify(verr.Errors)
	require.JSONEq(t, `{"query":"Validation failed (object is expected)"}`, s)
	require.Contains(t, verr.Error(), "object is expected")
}
