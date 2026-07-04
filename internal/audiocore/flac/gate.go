package flac

import (
	"os"
	"sync"
	"sync/atomic"
)

// envFlacEncoder names the environment variable that selects the FLAC export
// encoder. The value "native" enables the go-flac path; any other value (or
// unset) keeps the FFmpeg exporter.
//
// This is a temporary opt-in gate shared by every native FLAC path: the
// detection save path (EncodePCM) and the BirdWeather soundscape upload path.
// When the native encoder is trusted, the gate is removed and FLAC always routes
// to the native path. Both the detection save path and the BirdWeather path now
// normalize natively via audionorm (EBU R128); FFmpeg loudnorm remains only as a
// defensive fallback for a clip the native encoder cannot take (a non-16-bit
// depth, or a target/ceiling outside audionorm's range), neither of which a
// validated config reaches. Keep the read confined to NativeEncoderEnabled so the
// gate is a single deletion site.
const envFlacEncoder = "BIRDNET_FLAC_ENCODER"

// nativeEncoderValue is the only env value that enables the native encoder.
const nativeEncoderValue = "native"

var (
	nativeOnce    sync.Once
	nativeEnabled bool
	// nativeEnabledOverride, when it holds a non-nil pointer, forces the gate
	// value for tests in other packages that need to exercise the native path
	// deterministically (see SetNativeEncoderEnabledForTest). It is atomic because
	// NativeEncoderEnabled may be read from multiple goroutines. Empty in normal
	// operation.
	nativeEnabledOverride atomic.Pointer[bool]
)

// NativeEncoderEnabled reports whether the native go-flac export path is
// selected via the BIRDNET_FLAC_ENCODER environment variable. The variable is
// read once on first call; toggling it requires a restart.
func NativeEncoderEnabled() bool {
	if v := nativeEnabledOverride.Load(); v != nil {
		return *v
	}
	nativeOnce.Do(func() {
		nativeEnabled = os.Getenv(envFlacEncoder) == nativeEncoderValue
	})
	return nativeEnabled
}

// SetNativeEncoderEnabledForTest overrides the native-encoder gate for the
// duration of a test and returns a restore function that clears the override.
// It lets a test in another package drive the native FLAC path without depending
// on the BIRDNET_FLAC_ENCODER environment variable or the sync.Once cache. It is
// intended for tests only; production code must never call it.
func SetNativeEncoderEnabledForTest(enabled bool) (restore func()) {
	nativeEnabledOverride.Store(&enabled)
	return func() { nativeEnabledOverride.Store(nil) }
}
