package flac

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNativeEncoderEnabled(t *testing.T) {
	// Not parallel: mutates the package-level gate cache and the environment.
	cases := []struct {
		name string
		val  string
		want bool
	}{
		{"empty selects ffmpeg", "", false},
		{"native selects native", "native", true},
		{"ffmpeg selects ffmpeg", "ffmpeg", false},
		{"wrong case selects ffmpeg", "Native", false},
		{"truthy string selects ffmpeg", "1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset the cached sync.Once so the env var is re-read this run.
			nativeOnce = sync.Once{}
			t.Setenv(envFlacEncoder, tc.val)
			assert.Equal(t, tc.want, NativeEncoderEnabled())
		})
	}
}
