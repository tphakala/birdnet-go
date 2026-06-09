package embedding

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEncodeDecodeFP16ExactValues verifies that values which are exactly
// representable in IEEE-754 half precision survive an encode/decode round-trip
// bit-for-bit. This isolates codec correctness from rounding error.
func TestEncodeDecodeFP16ExactValues(t *testing.T) {
	t.Parallel()

	// All of these are exactly representable in fp16.
	vec := []float32{0, 1, -1, 0.5, -0.5, 0.25, 2, -4, 1024, -2048}

	blob, err := encodeVector(vec, FormatFP16)
	require.NoError(t, err)
	assert.Len(t, blob, len(vec)*fp16Bytes, "fp16 blob must be 2 bytes per element")

	got, err := decodeVector(blob, FormatFP16, len(vec))
	require.NoError(t, err)
	require.Len(t, got, len(vec))

	for i := range vec {
		assert.InDelta(t, vec[i], got[i], 0, "exactly representable value must round-trip bit-for-bit at index %d", i)
	}
}

// TestEncodeDecodeFP16Precision verifies that a vector of arbitrary values
// survives the round-trip with cosine similarity high enough to be
// near-lossless for similarity search, which is the reason fp16 is the default.
func TestEncodeDecodeFP16Precision(t *testing.T) {
	t.Parallel()

	vec := []float32{3.14159, -2.71828, 0.001, 42.5, -0.333333, 1280.7, 9.81, -100.25}

	blob, err := encodeVector(vec, FormatFP16)
	require.NoError(t, err)

	got, err := decodeVector(blob, FormatFP16, len(vec))
	require.NoError(t, err)
	require.Len(t, got, len(vec))

	assert.Greater(t, cosineSimilarity(vec, got), 0.9999,
		"fp16 round-trip must preserve cosine similarity for similarity search")
}

// cosineSimilarity is a test helper computing the cosine similarity between two
// equal-length float32 vectors.
func cosineSimilarity(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// TestEncodeVectorInt8Unsupported verifies that int8 encoding is gated behind
// the discriminator and rejected until the M0 separability probe validates it.
func TestEncodeVectorInt8Unsupported(t *testing.T) {
	t.Parallel()

	_, err := encodeVector([]float32{1, 2, 3}, FormatInt8)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedFormat)
}

// TestDecodeVectorDimMismatch verifies that a blob whose length does not match
// the declared dimension is rejected rather than silently truncated.
func TestDecodeVectorDimMismatch(t *testing.T) {
	t.Parallel()

	blob, err := encodeVector([]float32{1, 2, 3}, FormatFP16)
	require.NoError(t, err)

	_, err = decodeVector(blob, FormatFP16, 4)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCorruptBlob)
}
