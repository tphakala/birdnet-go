package api

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

// TestPatchAudioRejectsUnsafeExportPath verifies that an export path that
// escapes the clips directory, or carries a NUL byte, is rejected with a 400
// and that nothing is published: the controller's snapshot must be the very
// same pointer afterwards.
func TestPatchAudioRejectsUnsafeExportPath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "parent directory traversal", path: "../../../etc/passwd"},
		{name: "NUL byte", path: "clips\x00/etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)
			before := controller.Settings.Load()

			rec := sendSectionPatch(t, e, controller, "audio", map[string]any{
				"export": map[string]any{"path": tt.path},
			})

			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assert.Same(t, before, controller.Settings.Load(), "a rejected patch must not publish settings")
		})
	}
}

// TestPatchRejectsMistypedValues verifies that a value of the wrong JSON type,
// or an enum value outside the allowed set, is rejected with a 400 and that
// nothing is published. Some rows are rejected while the body is merged into the
// section and others by validation after the merge; the contract pinned here is
// that either way the live settings stay untouched.
func TestPatchRejectsMistypedValues(t *testing.T) {
	tests := []struct {
		name    string
		section string
		body    any
	}{
		{
			name:    "string instead of boolean",
			section: "mqtt",
			body:    map[string]any{"enabled": "yes"},
		},
		{
			name:    "number instead of string",
			section: "mqtt",
			body:    map[string]any{"broker": 12345},
		},
		{
			name:    "object instead of array",
			section: "rtsp",
			body:    map[string]any{"urls": map[string]string{"url1": "rtsp://localhost"}},
		},
		{
			name:    "array instead of object",
			section: "dashboard",
			body:    map[string]any{"thumbnails": []string{"summary", "recent"}},
		},
		{
			name:    "boolean instead of number",
			section: "birdnet",
			body:    map[string]any{"threshold": true},
		},
		{
			name:    "string instead of number",
			section: "audio",
			body: map[string]any{
				"export": map[string]any{"retention": map[string]any{"minClips": "ten"}},
			},
		},
		{
			name:    "invalid enum value",
			section: "audio",
			body:    map[string]any{"export": map[string]any{"type": "invalid_format"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)
			before := controller.Settings.Load()

			rec := sendSectionPatch(t, e, controller, tt.section, tt.body)

			assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assert.Same(t, before, controller.Settings.Load(), "a rejected patch must not publish settings")
		})
	}
}
