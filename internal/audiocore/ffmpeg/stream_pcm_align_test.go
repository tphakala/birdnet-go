package ffmpeg

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedReader hands out a fixed byte stream in the read sizes given by
// script, mimicking a pipe: stdout.Read returns whatever bytes happen to be
// available, which is not necessarily a whole number of PCM samples.
type scriptedReader struct {
	data   []byte
	script []int
	off    int
	step   int
}

func (s *scriptedReader) Read(p []byte) (int, error) {
	if s.off >= len(s.data) {
		return 0, io.EOF
	}
	n := len(s.data) - s.off
	if s.step < len(s.script) && s.script[s.step] < n {
		n = s.script[s.step]
	}
	if n > len(p) {
		n = len(p)
	}
	s.step++
	copied := copy(p, s.data[s.off:s.off+n])
	s.off += copied
	return copied, nil
}

func (s *scriptedReader) Close() error { return nil }

// pcmRamp builds a PCM16 byte stream whose every byte is distinct from its
// neighbours, so a one-byte shift in the framing cannot go unnoticed.
func pcmRamp(sampleCount int) []byte {
	data := make([]byte, sampleCount*2)
	for i := range sampleCount {
		v := int16((i*2731)%40000 - 20000) //nolint:gosec // G115: intentional wrap for a synthetic test signal
		if i%2 == 1 {
			v = -v
		}
		data[i*2] = byte(v)        //nolint:gosec // G115: intentional int16→byte truncation for PCM serialisation
		data[i*2+1] = byte(v >> 8) //nolint:gosec // G115: intentional int16→byte truncation for PCM serialisation
	}
	return data
}

// TestReadStdout_EmitsWholeFrames verifies that the FFmpeg reader never hands a
// partial PCM frame downstream, and never loses or reorders a byte doing so.
//
// A pipe read boundary is not a frame boundary, so a read that ends mid-frame is
// normal. Every downstream consumer (the router's EQ/gain and resampling paths,
// the sound level and audio level consumers, the analysis buffers) reinterprets
// these bytes as samples, and none of them can recover locally: dropping the
// remainder shifts every following byte, so each subsequent sample decodes with
// its halves swapped and turns into noise, while the resampler rejects the frame
// outright. Reassembly belongs at the producer.
//
// The cases cover every output format buildOutputArgs can select, because the
// frame size is derived from the configured bit depth and channel count. Mono
// PCM16 alone would pass even if the derivation were wrong, since its frame size
// happens to equal a single sample.
func TestReadStdout_EmitsWholeFrames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		bitDepth   int
		channels   int
		frameBytes int
	}{
		{"mono_s16le", 16, 1, 2},
		{"stereo_s16le", 16, 2, 4},
		{"mono_s24le", 24, 1, 3},
		{"stereo_s32le", 32, 2, 8},
		{"unsupported_depth_falls_back_to_s16le", 20, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Read sizes are deliberately coprime with the frame sizes under
			// test, so a read lands mid-frame repeatedly and the carry is
			// exercised on consecutive reads.
			script := []int{1001, 999, 3, 1, 2047, 4095}
			// A whole number of frames in total, so nothing is left in the
			// carry at EOF and the comparison can be exact.
			signal := pcmRamp(4096 * tc.frameBytes / 2)

			s := NewStream(&StreamConfig{
				URL:        "rtsp://example.invalid/stream",
				SourceID:   "align-src",
				SourceName: "align",
				Type:       "rtsp",
				SampleRate: 48000,
				BitDepth:   tc.bitDepth,
				Channels:   tc.channels,
			}, nil, nil, nil, nil)

			require.Equal(t, tc.frameBytes, pcmFrameBytes(&s.config),
				"frame size must follow the output format buildOutputArgs selects")

			readCh := make(chan readResult, 64)
			readerDone := make(chan struct{})
			t.Cleanup(func() { close(readerDone) })

			go s.readStdout(&scriptedReader{data: signal, script: script}, readCh, readerDone)

			var got []byte
			for res := range readCh {
				if res.err != nil {
					require.ErrorIs(t, res.err, io.EOF, "the scripted reader only ends with EOF")
					break
				}
				assert.Zero(t, len(res.data)%tc.frameBytes,
					"every frame handed downstream must hold whole PCM frames, got %d bytes for a %d-byte frame",
					len(res.data), tc.frameBytes)
				got = append(got, res.data...)
			}

			assert.Equal(t, signal, got,
				"the reassembled stream must match the source byte for byte; a mismatch means a byte was dropped or reordered")
		})
	}
}

// TestReadStdout_DropsOnlyTheFinalPartialFrame verifies the one case where bytes
// legitimately do not come out: a stream that ends mid-frame has nothing left to
// complete it with, so the remainder is discarded at EOF rather than held forever.
func TestReadStdout_DropsOnlyTheFinalPartialFrame(t *testing.T) {
	t.Parallel()

	signal := append(pcmRamp(512), 0x7f) // odd total: the last byte has no pair

	s := NewStream(&StreamConfig{
		URL:        "rtsp://example.invalid/stream",
		SourceID:   "align-src",
		SourceName: "align",
		Type:       "rtsp",
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
	}, nil, nil, nil, nil)

	readCh := make(chan readResult, 64)
	readerDone := make(chan struct{})
	t.Cleanup(func() { close(readerDone) })

	go s.readStdout(&scriptedReader{data: signal, script: []int{255, 513}}, readCh, readerDone)

	var got []byte
	for res := range readCh {
		if res.err != nil {
			break
		}
		got = append(got, res.data...)
	}

	assert.Equal(t, signal[:len(signal)-1], got,
		"everything but the unpaired trailing byte must be emitted")
}
