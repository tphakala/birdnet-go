//go:build !notflite

package hwprofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTFLiteLinked_DefaultBuild pins the compile-time guarantee that a normal
// build links the TFLite backend. It is the counterpart to the notflite-tagged
// test and keeps the API-level equality assertions from silently passing if a
// regression ever dropped TFLite from the default build.
func TestTFLiteLinked_DefaultBuild(t *testing.T) {
	require.True(t, TFLiteLinked(), "TFLiteLinked() must be true on a default (non-notflite) build")
}
