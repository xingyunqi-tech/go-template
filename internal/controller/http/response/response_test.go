package response_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
)

func TestWriteData(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	require.NoError(t, response.WriteData(rec, http.StatusCreated, map[string]string{"html": "<a&b>"}))

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, `{"code":200,"message":"success","data":{"html":"<a&b>"}}`, rec.Body.String())
}

func TestWriteOKOmitsData(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	require.NoError(t, response.WriteOK(rec, http.StatusOK))

	assert.Equal(t, `{"code":200,"message":"success"}`, rec.Body.String())
}

func TestWriteErrorNullData(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	require.NoError(t, response.WriteError(rec, http.StatusNotFound, "Cannot GET /x", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, `{"code":404,"message":"Cannot GET /x","data":null}`, rec.Body.String())
}
