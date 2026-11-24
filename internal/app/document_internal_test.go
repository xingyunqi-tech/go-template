package app

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	"github.com/xingyunqi-tech/go-template/openapi"
)

func TestTheIssuerServesTheWholeDocument(t *testing.T) {
	t.Parallel()

	pub, val, policies, err := servedDocuments(openapi.Document, httpapi.ValidationDocument, false)
	require.NoError(t, err)
	assert.Equal(t, openapi.Document, pub)
	assert.Equal(t, httpapi.ValidationDocument, val)
	assert.Len(t, policies, len(httpapi.Policies()))
}

func TestAVerifierLeavesOutTheIdentityOperations(t *testing.T) {
	t.Parallel()

	pub, val, policies, err := servedDocuments(openapi.Document, httpapi.ValidationDocument, true)
	require.NoError(t, err)

	for _, doc := range [][]byte{pub, val} {
		ids, err := operationIDs(doc)
		require.NoError(t, err)

		assert.True(t, ids["createNote"])
		assert.True(t, ids["uploadFile"])
		assert.True(t, ids["listAuditLogs"])
		assert.False(t, ids["loginWithEmail"])
		assert.False(t, ids["getMe"])
		assert.False(t, ids["listUsers"])
	}

	assert.Contains(t, policies, "getFileContent")
	assert.NotContains(t, policies, "refreshToken")

	var root struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}

	require.NoError(t, json.Unmarshal(pub, &root))
	assert.NotContains(t, root.Paths, "/api/v1/me/sessions/{id}", "paths left with only parameters are dropped")

	var components struct {
		Components struct {
			Schemas         map[string]json.RawMessage `json:"schemas"`
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
		} `json:"components"`
	}

	require.NoError(t, json.Unmarshal(pub, &components))
	assert.Contains(t, components.Components.Schemas, "Note")
	assert.Contains(t, components.Components.Schemas, "ErrorResponse")
	assert.NotContains(t, components.Components.Schemas, "LoginResponse")
	assert.NotContains(t, components.Components.Schemas, "User")
	assert.Contains(t, components.Components.SecuritySchemes, "bearerAuth")

	_, err = withoutTags([]byte("not json"), identityTags())
	require.Error(t, err)
}
