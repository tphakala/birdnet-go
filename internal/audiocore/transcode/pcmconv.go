package transcode

import (
	"io"
	"slices"
)

// widthConverter converts little-endian signed PCM of an arbitrary bit depth to
// 16-bit as it is read. FLAC emits ceil(bitDepth/8) bytes per sample, with the
// value right-aligned and sign-extended, so a 12-bit or 20-bit stream cannot be
// narrowed by taking its top bytes: each sample is decoded to an integer and
// shifted by bitDepth-16 (right for deeper, left for shallower streams).
type widthConverter struct {
	r        io.Reader
	srcWidth int // bytes per source sample
	shift    int // bitDepth - outputBitDepth
	scratch  []byte
}

// newWidthConverter wraps r, which yields samples of bitDepth bits.
func newWidthConverter(r io.Reader, bitDepth int) *widthConverter {
	return &widthConverter{
		r:        r,
		srcWidth: (bitDepth + 7) / 8,
		shift:    bitDepth - outputBitDepth,
	}
}

// Read fills p with 16-bit samples, reading source bytes in whole samples so the
// source width and the output width stay in step. len(p) is rounded down to whole
// samples. A source that ends inside a sample drops those trailing bytes.
func (c *widthConverter) Read(p []byte) (int, error) {
	samples := len(p) / bytesPerSample
	if samples == 0 {
		return 0, nil
	}
	need := samples * c.srcWidth
	if cap(c.scratch) < need {
		c.scratch = make([]byte, need)
	}
	buf := c.scratch[:need]

	got, err := io.ReadFull(c.r, buf)
	whole := got / c.srcWidth
	for i := range whole {
		v := signedLE(buf[i*c.srcWidth : (i+1)*c.srcWidth])
		if c.shift >= 0 {
			v >>= c.shift
		} else {
			v <<= -c.shift
		}
		p[i*bytesPerSample] = byte(v)
		p[i*bytesPerSample+1] = byte(v >> 8)
	}
	n := whole * bytesPerSample
	if err == io.ErrUnexpectedEOF {
		// ReadFull reports the stream ending after a partial buffer; the whole
		// samples it did return are valid and the next call reports io.EOF.
		err = nil
		if n == 0 {
			err = io.EOF
		}
	}
	return n, err
}

// signedLE decodes a little-endian signed integer of up to 4 bytes.
func signedLE(b []byte) int32 {
	var v uint32
	for _, octet := range slices.Backward(b) {
		v = v<<8 | uint32(octet)
	}
	// Sign-extend from the top bit of the last byte.
	shift := uint(32 - 8*len(b))
	return int32(v<<shift) >> shift //nolint:gosec // G115: intentional two's-complement reinterpretation
}
