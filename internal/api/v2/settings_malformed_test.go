package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// TestMalformedJSONData verifies the system handles malformed JSON gracefully
func TestMalformedJSONData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		section       string
		malformedData string
		expectedError string
	}{
		{
			name:          "Incomplete JSON object",
			section:       "dashboard",
			malformedData: `{"thumbnails": {"summary":`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Invalid JSON syntax",
			section:       "mqtt",
			malformedData: `{"enabled": true, "broker": }`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Trailing comma",
			section:       "weather",
			malformedData: `{"provider": "openweather",}`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Unquoted keys",
			section:       "birdnet",
			malformedData: `{latitude: 51.5074}`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Mixed single/double quotes",
			section:       "audio",
			malformedData: `{"export": {'type': "mp3"}}`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Unclosed string",
			section:       "mqtt",
			malformedData: `{"broker": "tcp://localhost:1883}`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Invalid escape sequence",
			section:       "dashboard",
			malformedData: `{"locale": "en\z"}`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Missing closing bracket",
			section:       "rtsp",
			malformedData: `{"urls": ["rtsp://localhost"`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Completely malformed JSON",
			section:       "species",
			malformedData: `{this is not json at all}`,
			expectedError: "Failed to parse request body",
		},
		{
			name:          "Invalid number format",
			section:       "birdnet",
			malformedData: `{"threshold": 0.1.2}`,
			expectedError: "Failed to parse request body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(t, e)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section,
				strings.NewReader(tt.malformedData))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err := controller.UpdateSectionSettings(ctx)

			// Use helper function to assert error response
			assertControllerError(t, err, rec, http.StatusBadRequest, tt.expectedError)
		})
	}
}
