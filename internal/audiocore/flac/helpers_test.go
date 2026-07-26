package flac

import "encoding/binary"

// pcm16 builds an interleaved little-endian PCM byte slice from int16 samples.
func pcm16(samples ...int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
	}
	return b
}
