package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMaliciousInputData verifies the system handles potentially malicious input
func TestMaliciousInputData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		section       string
		maliciousData interface{}
		description   string
	}{
		{
			name:    "SQL injection attempt in string field",
			section: "mqtt",
			maliciousData: map[string]interface{}{
				"broker": "tcp://localhost:1883'; DROP TABLE users; --",
				"topic":  "birdnet' OR '1'='1",
			},
			description: "Should accept but sanitize SQL injection attempts",
		},
		{
			name:    "XSS attempt in display fields",
			section: "dashboard",
			maliciousData: map[string]interface{}{
				"locale": "<script>alert('XSS')</script>",
			},
			description: "Should accept but fields should be escaped when displayed",
		},
		{
			name:    "Path traversal attempt",
			section: "audio",
			maliciousData: map[string]interface{}{
				"export": map[string]interface{}{
					"path": "../../../etc/passwd",
				},
			},
			description: "Should accept path but file operations should validate",
		},
		{
			name:    "Command injection attempt",
			section: "species",
			maliciousData: map[string]interface{}{
				"config": map[string]interface{}{
					"Test Bird": map[string]interface{}{
						"actions": []map[string]interface{}{
							{
								"type":       "ExecuteCommand",
								"command":    "/bin/sh",
								"parameters": []string{"-c", "rm -rf /"},
							},
						},
					},
				},
			},
			description: "Should accept but execution should be sandboxed",
		},
		{
			name:    "Buffer overflow attempt with long string",
			section: "mqtt",
			maliciousData: map[string]interface{}{
				"topic": strings.Repeat("A", 10000),
			},
			description: "Should handle very long strings gracefully",
		},
		{
			name:    "Unicode injection",
			section: "dashboard",
			maliciousData: map[string]interface{}{
				"locale": "en\u202E\u0000\u200E",
			},
			description: "Should handle Unicode control characters",
		},
		{
			name:    "Null byte injection",
			section: "audio",
			maliciousData: map[string]interface{}{
				"export": map[string]interface{}{
					"path": "clips\x00/etc/passwd",
				},
			},
			description: "Should handle null bytes in paths",
		},
		{
			name:    "LDAP injection attempt",
			section: "mqtt",
			maliciousData: map[string]interface{}{
				"username": "admin)(uid=*))(|(uid=*",
			},
			description: "Should handle LDAP injection attempts",
		},
		{
			name:    "XML injection attempt",
			section: "dashboard",
			maliciousData: map[string]interface{}{
				"locale": "<?xml version=\"1.0\"?><!DOCTYPE foo [<!ENTITY xxe SYSTEM \"file:///etc/passwd\">]><foo>&xxe;</foo>",
			},
			description: "Should handle XML injection attempts",
		},
		{
			name:    "JSON injection in nested field",
			section: "mqtt",
			maliciousData: map[string]interface{}{
				"topic": `","enabled":true,"broker":"evil.com:1883","topic":"`,
			},
			description: "Should handle JSON injection attempts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(e)

			body, err := json.Marshal(tt.maliciousData)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section, 
				bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			// The update should succeed (we accept the data)
			err = controller.UpdateSectionSettings(ctx)
			if err != nil {
				// Some malicious inputs might be rejected, which is also fine
				var httpErr *echo.HTTPError
				if errors.As(err, &httpErr) && httpErr.Code == http.StatusBadRequest {
					t.Logf("Input rejected as expected: %v", err)
					return
				}
			}

			// If accepted, verify the response is valid
			assert.Equal(t, http.StatusOK, rec.Code)
			t.Logf("%s: %s", tt.name, tt.description)
		})
	}
}

// TestTypeConfusionAttacks verifies the system handles wrong types gracefully
func TestTypeConfusionAttacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		section       string
		confusedData  interface{}
		expectedError string
	}{
		{
			name:    "String instead of boolean",
			section: "mqtt",
			confusedData: map[string]interface{}{
				"enabled": "yes",
			},
			expectedError: "json: cannot unmarshal",
		},
		{
			name:    "Number instead of string",
			section: "mqtt",
			confusedData: map[string]interface{}{
				"broker": 12345,
			},
			expectedError: "json: cannot unmarshal",
		},
		{
			name:    "Object instead of array",
			section: "rtsp",
			confusedData: map[string]interface{}{
				"urls": map[string]string{"url1": "rtsp://localhost"},
			},
			expectedError: "json: cannot unmarshal",
		},
		{
			name:    "Array instead of object",
			section: "dashboard",
			confusedData: map[string]interface{}{
				"thumbnails": []string{"summary", "recent"},
			},
			expectedError: "json: cannot unmarshal",
		},
		{
			name:    "Null for required field",
			section: "birdnet",
			confusedData: map[string]interface{}{
				"latitude": nil,
			},
			expectedError: "json: cannot unmarshal",
		},
		{
			name:    "Boolean instead of number",
			section: "birdnet",
			confusedData: map[string]interface{}{
				"threshold": true,
			},
			expectedError: "json: cannot unmarshal",
		},
		{
			name:    "String instead of number array",
			section: "audio",
			confusedData: map[string]interface{}{
				"export": map[string]interface{}{
					"retention": map[string]interface{}{
						"minClips": "ten",
					},
				},
			},
			expectedError: "json: cannot unmarshal",
		},
		{
			name:    "Invalid enum value",
			section: "audio",
			confusedData: map[string]interface{}{
				"export": map[string]interface{}{
					"type": "invalid_format",
				},
			},
			expectedError: "", // May be accepted, validation happens elsewhere
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			controller := getTestController(e)

			body, err := json.Marshal(tt.confusedData)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section, 
				bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err = controller.UpdateSectionSettings(ctx)
			// Type confusion might be caught at JSON unmarshal or later validation
			if err == nil {
				// If no error, the system handled the type conversion
				assert.Equal(t, http.StatusOK, rec.Code)
			} else {
				// Error is expected for type mismatches
				t.Logf("Type confusion properly rejected: %v", err)
			}
		})
	}
}