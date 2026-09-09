package formatutil

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBytes verifies the signed human-readable byte rendering, including the
// sub-unit, exact-boundary, and each magnitude step, plus the negative case
// that documents the "printed verbatim" contract.
func TestBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"zero", 0, "0 B"},
		{"sub-kib", 512, "512 B"},
		{"one-kib-boundary", 1024, "1.0 KB"},
		{"kilobytes", 1536, "1.5 KB"},
		{"megabytes", 150 * 1024 * 1024, "150.0 MB"},
		{"gigabytes", 18 * 1024 * 1024 * 1024, "18.0 GB"},
		{"negative-verbatim", -5, "-5 B"},
		{"max-int64-stays-in-range", math.MaxInt64, "8.0 EB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Bytes(tt.bytes))
		})
	}
}

// TestBytesUint64 verifies the unsigned variant renders identically to Bytes
// for shared magnitudes and stays bounded at the top of the uint64 range.
func TestBytesUint64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bytes uint64
		want  string
	}{
		{"zero", 0, "0 B"},
		{"sub-kib", 512, "512 B"},
		{"one-kib-boundary", 1024, "1.0 KB"},
		{"kilobytes", 1536, "1.5 KB"},
		{"megabytes", 150 * 1024 * 1024, "150.0 MB"},
		{"gigabytes", 18 * 1024 * 1024 * 1024, "18.0 GB"},
		{"max-uint64-stays-in-range", math.MaxUint64, "16.0 EB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, BytesUint64(tt.bytes))
		})
	}
}
