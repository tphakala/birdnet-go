// Package audiocore, capture_test.go.
// Unit tests for the conversion helpers used by the malgo capture callback.
package audiocore

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/gen2brain/malgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertInto_MatchesAllocating verifies that every Into conversion
// variant produces byte-identical output to the allocating wrapper for a
// representative set of inputs. The Into variants are the pool-friendly
// path used by startCapture when a bufMgr is wired.
func TestConvertInto_MatchesAllocating(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// makeIn returns the raw input bytes for one synthetic frame.
		makeIn   func() []byte
		format   malgo.FormatType
		outBytes int // expected output byte count
	}{
		{
			name: "S16 passthrough copies",
			makeIn: func() []byte {
				return []byte{0x01, 0x00, 0x02, 0x00, 0xFF, 0x7F}
			},
			format:   malgo.FormatS16,
			outBytes: 6,
		},
		{
			name: "S24 to S16",
			makeIn: func() []byte {
				// 2 samples, 3 bytes each.
				return []byte{0x00, 0x00, 0x7F, 0x00, 0x00, 0x80}
			},
			format:   malgo.FormatS24,
			outBytes: 4,
		},
		{
			name: "S32 to S16",
			makeIn: func() []byte {
				// 2 samples, 4 bytes each, positive and negative clips.
				return []byte{0x00, 0x00, 0x00, 0x7F, 0x00, 0x00, 0x00, 0x80}
			},
			format:   malgo.FormatS32,
			outBytes: 4,
		},
		{
			name: "F32 to S16",
			makeIn: func() []byte {
				// Two float32 values: +1.0 and -1.0.
				buf := make([]byte, 8)
				binary.LittleEndian.PutUint32(buf[0:4], math.Float32bits(1.0))
				binary.LittleEndian.PutUint32(buf[4:8], math.Float32bits(-1.0))
				return buf
			},
			format:   malgo.FormatF32,
			outBytes: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := tt.makeIn()

			// Allocating reference output.
			refOut, _ := convertToS16IfNeeded(in, tt.format, 16)
			require.Len(t, refOut, tt.outBytes)

			// s16OutputSize must agree with the allocating helper.
			assert.Equal(t, tt.outBytes, s16OutputSize(in, tt.format),
				"s16OutputSize must match the allocating helper")

			// Into variant: caller-owned dst of the expected size.
			dst := make([]byte, tt.outBytes)
			convertToS16IfNeededInto(dst, in, tt.format)
			assert.Equal(t, refOut, dst, "Into variant must produce byte-identical output")
		})
	}
}
