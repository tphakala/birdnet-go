package apicore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRestoreRedactedSecret verifies the single-field restore primitive shared
// by the settings save flow and the integration test-connection handlers.
func TestRestoreRedactedSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		current  string
		incoming string
		want     string
	}{
		{
			name:     "placeholder is restored to current real secret",
			current:  "real-saved-key",
			incoming: RedactedValue,
			want:     "real-saved-key",
		},
		{
			name:     "real incoming value is left untouched",
			current:  "real-saved-key",
			incoming: "user-typed-new-key",
			want:     "user-typed-new-key",
		},
		{
			name:     "empty incoming stays empty (not a placeholder)",
			current:  "real-saved-key",
			incoming: "",
			want:     "",
		},
		{
			name:     "placeholder with empty current resolves to empty",
			current:  "",
			incoming: RedactedValue,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			incoming := tt.incoming
			RestoreRedactedSecret(tt.current, &incoming)
			assert.Equal(t, tt.want, incoming)
		})
	}

	t.Run("nil incoming is a no-op", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() { RestoreRedactedSecret("real-saved-key", nil) })
	})
}
