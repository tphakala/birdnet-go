// Package cpuspec provides CPU feature detection for BirdNET-Go backend selection.
package cpuspec

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHasNativeF16_NonArm64IsFalse(t *testing.T) {
	t.Parallel()
	// On any non-(linux/arm64) build host (e.g. the amd64 CI runner), the
	// predicate must be false: OpenVINO f16 is strictly A76+/ARMv8.2.
	if runtime.GOARCH != "arm64" || runtime.GOOS != "linux" {
		assert.False(t, HasNativeF16(), "HasNativeF16 must be false off linux/arm64")
	} else {
		t.Skip("on linux/arm64 HasNativeF16 depends on CPU HWCAP; no fixed assertion")
	}
}

func TestHasNativeF16_DoesNotPanic(t *testing.T) {
	t.Parallel()
	// Must be safe to call on every platform.
	_ = HasNativeF16()
}
