package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPing(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/ping", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/ping")

	// Execute
	require.NoError(t, controller.Ping(c))

	// Assert status code
	assert.Equal(t, http.StatusOK, rec.Code)

	// Assert content type
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	// Assert response body
	var response map[string]string
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response["status"])
	assert.Len(t, response, 1, "Response should only contain 'status' field")
}
