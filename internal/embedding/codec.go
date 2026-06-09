// Package embedding provides generic persistence and read access for model
// embedding vectors. Raw vector blobs are the source of truth; a format
// discriminator records the on-disk encoding so additional encodings can be
// added later without migrating existing rows.
package embedding

import (
	"encoding/binary"

	"github.com/x448/float16"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Format identifies the on-disk encoding of a stored vector blob. It is
// persisted alongside every row so the decoder can dispatch correctly and new
// encodings can be added additively without a schema migration.
type Format string

const (
	// FormatFP16 stores each component as an IEEE-754 half-precision value
	// (little-endian uint16). It is near-lossless for cosine similarity and
	// half the size of float32, so it is the default encoding.
	FormatFP16 Format = "fp16"

	// FormatInt8 is reserved for an optional quantized encoding. It is gated
	// behind the discriminator and not yet implemented; it only becomes
	// available once the offline separability probe validates that int8
	// preserves enough signal for downstream consumers.
	FormatInt8 Format = "int8"
)

// fp16Bytes is the on-disk size in bytes of a single fp16-encoded component.
const fp16Bytes = 2

// Sentinel errors returned by the codec. They are prefixed with Err per the
// project's error-naming convention and are safe to test with errors.Is.
var (
	// ErrUnsupportedFormat is returned when a vector is encoded or decoded
	// using a Format that is recognised by the discriminator but not yet
	// implemented (currently FormatInt8).
	ErrUnsupportedFormat = errors.Newf("embedding: unsupported vector format").
				Component("embedding").
				Category(errors.CategoryValidation).
				Build()

	// ErrCorruptBlob is returned when a stored blob cannot be decoded into the
	// declared dimension, for example because its length is inconsistent with
	// the encoding.
	ErrCorruptBlob = errors.Newf("embedding: corrupt vector blob").
			Component("embedding").
			Category(errors.CategoryValidation).
			Build()
)

// encodeVector encodes a float32 vector into a byte blob using the given
// format. The returned blob is the raw source-of-truth representation written
// to storage.
func encodeVector(vec []float32, format Format) ([]byte, error) {
	switch format {
	case FormatFP16:
		blob := make([]byte, len(vec)*fp16Bytes)
		for i, v := range vec {
			bits := float16.Fromfloat32(v).Bits()
			binary.LittleEndian.PutUint16(blob[i*fp16Bytes:], bits)
		}
		return blob, nil
	case FormatInt8:
		return nil, ErrUnsupportedFormat
	default:
		return nil, ErrUnsupportedFormat
	}
}

// decodeVector decodes a byte blob of the given format back into a float32
// vector of the declared dimension. It returns ErrCorruptBlob when the blob
// length is inconsistent with the format and dimension.
func decodeVector(blob []byte, format Format, dim int) ([]float32, error) {
	switch format {
	case FormatFP16:
		if len(blob) != dim*fp16Bytes {
			return nil, ErrCorruptBlob
		}
		vec := make([]float32, dim)
		for i := range dim {
			bits := binary.LittleEndian.Uint16(blob[i*fp16Bytes:])
			vec[i] = float16.Frombits(bits).Float32()
		}
		return vec, nil
	case FormatInt8:
		return nil, ErrUnsupportedFormat
	default:
		return nil, ErrUnsupportedFormat
	}
}
