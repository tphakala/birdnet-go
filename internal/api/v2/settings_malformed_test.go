package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

// TestMalformedJSONData verifies a PATCH body that is not valid JSON is
// rejected with 400 and the "Failed to parse request body" message.
//
// parseAndValidateJSON (settings.go) can fail in two places: the request bind
// and the follow-up json.Unmarshal. Every syntax error fails the bind the same
// way, so the table keeps one truncated body, one body with a syntax error, and
// the empty body, which is the shape that reaches the second check.
//
// The empty body is the one shape that skips the bind: echo returns early for
// a zero ContentLength (echo v4.16.0 bind.go:78), so the Unmarshal check
// rejects it.
//
// Not parallel: see the note in settings_blocked_fields_test.go.
func TestMalformedJSONData(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "truncated body", body: `{"thumbnails": {"summary":`},
		{name: "invalid syntax", body: `{"enabled": true, "broker": }`},
		{name: "empty body", body: ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/dashboard", strings.NewReader(tt.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues("dashboard")

			// HandleError writes the 400 and returns nil.
			require.NoError(t, controller.UpdateSectionSettings(ctx))
			assert.Equal(t, http.StatusBadRequest, rec.Code)

			var response apicore.ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response), "the error body must be JSON")
			assert.Contains(t, response.Message, "Failed to parse request body")
		})
	}
}
