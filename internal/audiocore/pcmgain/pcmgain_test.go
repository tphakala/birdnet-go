package pcmgain

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

func TestApplyInt16_IdentityAtFactorOne(t *testing.T) {
	t.Parallel()
	src := pcm16(0, 1, -1, 100, -100, 32767, -32768)
	dst := make([]byte, len(src))
	ApplyInt16(dst, src, 1.0)
	assert.Equal(t, src, dst)
}

func TestApplyInt16_ScalesUp(t *testing.T) {
	t.Parallel()
	src := pcm16(100, -100, 1000)
	dst := make([]byte, len(src))
	ApplyInt16(dst, src, 2.0)
	assert.Equal(t, []int16{200, -200, 2000}, samples16(dst))
}

func TestApplyInt16_ScalesDown(t *testing.T) {
	t.Parallel()
	src := pcm16(100, -100, 1000)
	dst := make([]byte, len(src))
	ApplyInt16(dst, src, 0.5)
	assert.Equal(t, []int16{50, -50, 500}, samples16(dst))
}

func TestApplyInt16_SaturatesPositive(t *testing.T) {
	t.Parallel()
	src := pcm16(20000, 32767)
	dst := make([]byte, len(src))
	ApplyInt16(dst, src, 4.0)
	assert.Equal(t, []int16{32767, 32767}, samples16(dst))
}

func TestApplyInt16_SaturatesNegative(t *testing.T) {
	t.Parallel()
	src := pcm16(-20000, -32768)
	dst := make([]byte, len(src))
	ApplyInt16(dst, src, 4.0)
	assert.Equal(t, []int16{-32768, -32768}, samples16(dst))
}

func TestApplyInt16_DoesNotMutateSource(t *testing.T) {
	t.Parallel()
	src := pcm16(100, -100, 1000)
	srcCopy := bytes.Clone(src)
	dst := make([]byte, len(src))
	ApplyInt16(dst, src, 2.0)
	assert.Equal(t, srcCopy, src, "source buffer must be unchanged")
}

// A trailing odd byte (never produced by real int16 PCM, but defensive) is
// copied through unchanged so dst is always fully written.
func TestApplyInt16_CopiesOddTrailingByte(t *testing.T) {
	t.Parallel()
	src := []byte{0x10, 0x00, 0x20, 0x00, 0xAB}
	dst := make([]byte, len(src))
	ApplyInt16(dst, src, 1.0)
	assert.Equal(t, src, dst)
}

func TestApplyInt16_ZeroAlloc(t *testing.T) {
	// No t.Parallel(): testing.AllocsPerRun panics if called from a parallel test.
	// 4096 samples (8192 bytes) of arbitrary content: a realistic span for the
	// loop; the byte values are irrelevant to allocation behavior.
	src := make([]byte, 4096*2)
	for i := range src {
		src[i] = byte(i)
	}
	dst := make([]byte, len(src))
	allocs := testing.AllocsPerRun(100, func() {
		ApplyInt16(dst, src, 1.5)
	})
	assert.Zero(t, allocs, "ApplyInt16 must not allocate")
}

// FactorFromDB is the dB law every native encoder's gain goes through, so it is
// pinned against independently computed literals rather than against another
// call to the production code. An earlier version of the FLAC test derived its
// expected buffer by calling FactorFromDB itself, which made the assertion
// f(x) == f(x): swapping the exponent to gainDB/10 left the whole codec suite
// green. These literals are 10^(dB/20) computed by hand.
func TestFactorFromDB(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		gainDB float64
		want   float64
	}{
		{name: "unity at zero", gainDB: 0, want: 1},
		{name: "minus 6 dB halves amplitude", gainDB: -6, want: 0.5011872336272722},
		{name: "plus 6 dB doubles amplitude", gainDB: 6, want: 1.9952623149688795},
		{name: "minus 20 dB is one tenth", gainDB: -20, want: 0.1},
		{name: "plus 20 dB is ten times", gainDB: 20, want: 10},
		{name: "minus 3 dB", gainDB: -3, want: 0.7079457843841379},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.want, FactorFromDB(tt.gainDB), 1e-12)
		})
	}
}

// Applied returns the source itself at 0 dB so the common no-gain export stays
// zero-copy, and a fresh buffer otherwise. Callers rely on both halves: the
// aliasing one because the same PCM is handed to the spectrogram pre-renderer
// afterwards, the copying one because the source must not be mutated.
func TestApplied(t *testing.T) {
	t.Parallel()

	t.Run("zero dB aliases the source", func(t *testing.T) {
		t.Parallel()
		src := pcm16(100, -100, 1000)
		want := bytes.Clone(src)
		got := Applied(src, 0)
		// assert.Same compares addresses. assert.Equal would not: on two *byte it
		// falls through to reflect.DeepEqual, which compares the pointed-to bytes,
		// so it passes even when Applied copies.
		assert.Same(t, &src[0], &got[0], "0 dB must not copy")
		// Aliasing alone is not enough: returning the source but having scribbled
		// on it would still satisfy the pointer check.
		assert.Equal(t, want, got, "0 dB must leave the samples untouched")
	})

	t.Run("non-zero dB copies and leaves the source intact", func(t *testing.T) {
		t.Parallel()
		src := pcm16(100, -100, 1000)
		srcCopy := bytes.Clone(src)
		got := Applied(src, -6)
		assert.NotSame(t, &src[0], &got[0], "a gained result must be a new buffer")
		assert.Equal(t, srcCopy, src, "source must be unchanged")
		assert.Equal(t, []int16{50, -50, 501}, samples16(got))
	})
}
