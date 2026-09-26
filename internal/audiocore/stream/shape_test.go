package stream

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// s16le builds an interleaved little-endian s16 buffer from sample values.
func s16le(samples ...int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
	}
	return b
}

// monoSamples decodes an s16le buffer back to int16 values.
func monoSamples(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

func TestShapeToMono(t *testing.T) {
	tests := []struct {
		name        string
		src         []byte
		srcChannels int
		mode        string
		want        []int16
	}{
		{
			name:        "mono passthrough",
			src:         s16le(10, 20, 30),
			srcChannels: 1,
			mode:        channelModeDownmix,
			want:        []int16{10, 20, 30},
		},
		{
			name:        "stereo downmix averages channels",
			src:         s16le(100, 200, -100, 100), // (L,R),(L,R)
			srcChannels: 2,
			mode:        channelModeDownmix,
			want:        []int16{150, 0},
		},
		{
			name:        "empty mode defaults to downmix",
			src:         s16le(100, 200),
			srcChannels: 2,
			mode:        "",
			want:        []int16{150},
		},
		{
			name:        "stereo left picks channel 0",
			src:         s16le(100, 200, 300, 400),
			srcChannels: 2,
			mode:        channelModeLeft,
			want:        []int16{100, 300},
		},
		{
			name:        "stereo right picks channel 1",
			src:         s16le(100, 200, 300, 400),
			srcChannels: 2,
			mode:        channelModeRight,
			want:        []int16{200, 400},
		},
		{
			name:        "four channel downmix averages all",
			src:         s16le(100, 200, 300, 400),
			srcChannels: 4,
			mode:        channelModeDownmix,
			want:        []int16{250},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shapeToMono(nil, tt.src, tt.srcChannels, tt.mode)
			assert.Equal(t, tt.want, monoSamples(got))
		})
	}
}

func TestShapeToMono_reusesDstCapacity(t *testing.T) {
	dst := make([]byte, 0, 64)
	got := shapeToMono(dst, s16le(100, 200), 2, channelModeDownmix)
	require.Equal(t, []int16{150}, monoSamples(got))
	require.NotEmpty(t, got)
	// The result must be written into dst's backing array, not a fresh
	// allocation. Compare the backing pointers as addresses: comparing *byte
	// with assert.Equal routes through reflect.DeepEqual, which compares the
	// pointed-to values (both 150 here) and would pass even for a fresh buffer.
	assert.Equal(t,
		uintptr(unsafe.Pointer(unsafe.SliceData(dst[:1]))),
		uintptr(unsafe.Pointer(unsafe.SliceData(got))),
		"shapeToMono should reuse dst's backing array")
}
