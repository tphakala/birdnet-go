package conf

import "testing"

func TestNativeAACEncoderEnabled(t *testing.T) {
	assertNativeGate(t, EnvNativeAACEncoder, NativeAACEncoderEnabled)
}

// AAC is now the only gated native encoder: go-opus (Opus) and go-hls (HLS) are
// both unconditional defaults, so their gates have been removed.
