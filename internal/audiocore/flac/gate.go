package flac

import (
	"os"
	"sync"
)

// envFlacEncoder names the environment variable that selects the FLAC export
// encoder. The value "native" enables the go-flac path; any other value (or
// unset) keeps the FFmpeg exporter.
//
// This is a temporary opt-in gate. When the native encoder is trusted, the gate
// is removed and FLAC always routes to the native path (normalization still
// falls back to FFmpeg). Keep the read confined to NativeEncoderEnabled so the
// gate is a single deletion site.
const envFlacEncoder = "BIRDNET_FLAC_ENCODER"

// nativeEncoderValue is the only env value that enables the native encoder.
const nativeEncoderValue = "native"

var (
	nativeOnce    sync.Once
	nativeEnabled bool
)

// NativeEncoderEnabled reports whether the native go-flac export path is
// selected via the BIRDNET_FLAC_ENCODER environment variable. The variable is
// read once on first call; toggling it requires a restart.
func NativeEncoderEnabled() bool {
	nativeOnce.Do(func() {
		nativeEnabled = os.Getenv(envFlacEncoder) == nativeEncoderValue
	})
	return nativeEnabled
}
