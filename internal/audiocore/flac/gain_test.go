package flac

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

// pcm16 builds an interleaved little-endian PCM byte slice from int16 samples.
func pcm16(samples ...int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
	}
	return b
}

// samples16 decodes a little-endian PCM byte slice back to int16 samples.
func samples16(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

func TestApplyGainInt16_IdentityAtFactorOne(t *testing.T) {
	t.Parallel()
	src := pcm16(0, 1, -1, 100, -100, 32767, -32768)
	dst := make([]byte, len(src))
	applyGainInt16(dst, src, 1.0)
	assert.Equal(t, src, dst)
}

func TestApplyGainInt16_ScalesUp(t *testing.T) {
	t.Parallel()
	src := pcm16(100, -100, 1000)
	dst := make([]byte, len(src))
	applyGainInt16(dst, src, 2.0)
	assert.Equal(t, []int16{200, -200, 2000}, samples16(dst))
}

func TestApplyGainInt16_ScalesDown(t *testing.T) {
	t.Parallel()
	src := pcm16(100, -100, 1000)
	dst := make([]byte, len(src))
	applyGainInt16(dst, src, 0.5)
	assert.Equal(t, []int16{50, -50, 500}, samples16(dst))
}

func TestApplyGainInt16_SaturatesPositive(t *testing.T) {
	t.Parallel()
	src := pcm16(20000, 32767)
	dst := make([]byte, len(src))
	applyGainInt16(dst, src, 4.0)
	assert.Equal(t, []int16{32767, 32767}, samples16(dst))
}

func TestApplyGainInt16_SaturatesNegative(t *testing.T) {
	t.Parallel()
	src := pcm16(-20000, -32768)
	dst := make([]byte, len(src))
	applyGainInt16(dst, src, 4.0)
	assert.Equal(t, []int16{-32768, -32768}, samples16(dst))
}

func TestApplyGainInt16_DoesNotMutateSource(t *testing.T) {
	t.Parallel()
	src := pcm16(100, -100, 1000)
	srcCopy := bytes.Clone(src)
	dst := make([]byte, len(src))
	applyGainInt16(dst, src, 2.0)
	assert.Equal(t, srcCopy, src, "source buffer must be unchanged")
}

// A trailing odd byte (never produced by real int16 PCM, but defensive) is
// copied through unchanged so dst is always fully written.
func TestApplyGainInt16_CopiesOddTrailingByte(t *testing.T) {
	t.Parallel()
	src := []byte{0x10, 0x00, 0x20, 0x00, 0xAB}
	dst := make([]byte, len(src))
	applyGainInt16(dst, src, 1.0)
	assert.Equal(t, src, dst)
}

func TestApplyGainInt16_ZeroAlloc(t *testing.T) {
	// No t.Parallel(): testing.AllocsPerRun panics if called from a parallel test.
	// 4096 samples (8192 bytes) of arbitrary content: a realistic span for the
	// loop; the byte values are irrelevant to allocation behavior.
	src := make([]byte, 4096*2)
	for i := range src {
		src[i] = byte(i)
	}
	dst := make([]byte, len(src))
	allocs := testing.AllocsPerRun(100, func() {
		applyGainInt16(dst, src, 1.5)
	})
	assert.Zero(t, allocs, "applyGainInt16 must not allocate")
}
