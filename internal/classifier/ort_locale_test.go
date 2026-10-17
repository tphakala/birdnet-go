package classifier

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestORTUnavailableMessage_LocalesUseBareMinimumParam pins the contract between
// the notification parameter and the locale text: the Go side passes the bare
// minimum ("1.29"), so the English string must supply the "or newer" wording
// itself and every locale must keep the {requiredVersion} placeholder.
func TestORTUnavailableMessage_LocalesUseBareMinimumParam(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("../../frontend/static/messages/??.json")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(file) //nolint:gosec // repo-relative test input
			require.NoError(t, err)

			var doc struct {
				Notifications struct {
					Content struct {
						ORT struct {
							UnavailableMessage string `json:"unavailableMessage"`
						} `json:"ort"`
					} `json:"content"`
				} `json:"notifications"`
			}
			require.NoError(t, json.Unmarshal(data, &doc))

			msg := doc.Notifications.Content.ORT.UnavailableMessage
			assert.Contains(t, msg, "{requiredVersion}")
			assert.NotContains(t, msg, "1.29", "the version comes from the parameter")
			if filepath.Base(file) == "en.json" {
				assert.Contains(t, msg, "{requiredVersion} or newer")
			}
		})
	}
}
