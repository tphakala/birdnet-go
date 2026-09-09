//go:build notflite

package hwprofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTFLiteLinked_NotfliteBuild pins the compile-time guarantee that the
// notflite build tag produces a strictly-ONNX build reporting TFLite as not
// linked, so the tflite capability token is never emitted for a binary with no
// TFLite runtime.
func TestTFLiteLinked_NotfliteBuild(t *testing.T) {
	require.False(t, TFLiteLinked(), "TFLiteLinked() must be false under the notflite build tag")
}
