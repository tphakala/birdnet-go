package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// finalizeValidation classifies the outcome of ValidateSettings. Severity must
// be decided structurally: any error a validator returns is fatal. A finding
// must never be demoted to a non-fatal warning just because its human-readable
// message happens to contain a particular substring. These tests pin that
// contract so the old substring heuristic cannot creep back in.
func TestFinalizeValidation_FindingsStayFatalRegardlessOfMessage(t *testing.T) {
	t.Parallel()
	// Each message contains a substring the removed heuristic treated as a
	// non-fatal warning ("fallback", "not supported", "OAuth authentication
	// warning"). A validator returning any of these as an error means a real,
	// fatal misconfiguration and must block startup.
	markerMessages := []string{
		`embeddings.storage.format "int8" is not supported`,
		"spectrogram mode invalid, no fallback available",
		"OAuth authentication warning: redirect host missing",
	}
	for _, msg := range markerMessages {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()
			err := finalizeValidation(ValidationError{Errors: []string{msg}})
			require.Error(t, err, "validation finding must stay fatal regardless of message text")
		})
	}
}

func TestFinalizeValidation_NilWhenValid(t *testing.T) {
	t.Parallel()
	assert.NoError(t, finalizeValidation(nil))
}

func TestFinalizeValidation_NonValidationErrorIsFatal(t *testing.T) {
	t.Parallel()
	err := finalizeValidation(errors.Newf("some unrelated error").Build())
	require.Error(t, err)
}

func TestDefaultConfigCreatedMarker(t *testing.T) {
	// Not parallel: mutates package-level marker state. Reset at entry and
	// exit so the test is order-independent regardless of what other tests
	// in the package did with the marker.
	defaultConfigCreatedPath.Store(nil)
	t.Cleanup(func() { defaultConfigCreatedPath.Store(nil) })

	created, path := DefaultConfigCreated()
	assert.False(t, created, "marker starts unset")
	assert.Empty(t, path)

	markDefaultConfigCreated("/tmp/config.yaml")
	created, path = DefaultConfigCreated()
	assert.True(t, created)
	assert.Equal(t, "/tmp/config.yaml", path)
}
