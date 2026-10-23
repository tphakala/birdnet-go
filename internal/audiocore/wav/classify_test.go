package wav

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestClassifyEncodeError verifies that a write fault stays a file I/O error and
// any other encoder error is an audio error carrying the stream parameters.
func TestClassifyEncodeError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		category errors.ErrorCategory
	}{
		{"write fault", fmt.Errorf("go-wav: write: %w", &os.PathError{Op: "write", Path: "clip.wav.tmp", Err: os.ErrClosed}), errors.CategoryFileIO},
		{"encoder rejection", fmt.Errorf("go-wav: frame size is not positive"), errors.CategoryAudio},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var enhanced *errors.EnhancedError
			require.ErrorAs(t, classifyEncodeError(tt.err, 48000, 16), &enhanced)
			assert.Equal(t, string(tt.category), enhanced.GetCategory())
			assert.Equal(t, "audiocore/wav", enhanced.GetComponent())
		})
	}
}
