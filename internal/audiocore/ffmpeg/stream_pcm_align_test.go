package ffmpeg

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

// errScriptedFailure is the non-EOF read failure used to check that a pending
// carry is discarded rather than emitted when the stream breaks.
var errScriptedFailure = errors.New("scripted read failure")

// scriptedReader hands out a fixed byte stream in the read sizes given by
// script, mimicking a pipe: stdout.Read returns whatever bytes happen to be
// available, which is not necessarily a whole number of PCM frames.
//
// failAtEnd makes the final call return errScriptedFailure instead of io.EOF, so
// a test can drive the "read fails while a carry is pending" path.
type scriptedReader struct {
	data      []byte
	script    []int
	off       int
	step      int
	failAtEnd bool
}

// newScriptedReader validates the script before use. A zero or negative entry
// would make Read return (0, nil) forever, hanging the test instead of failing
// it, so it is rejected up front rather than left as a trap.
func newScriptedReader(t *testing.T, data []byte, script []int) *scriptedReader {
	t.Helper()
	for i, n := range script {
		require.Positive(t, n, "scripted read size %d must be positive", i)
	}
	return &scriptedReader{data: data, script: script}
}

func (s *scriptedReader) Read(p []byte) (int, error) {
	if s.off >= len(s.data) {
		if s.failAtEnd {
			return 0, errScriptedFailure
		}
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

// pcmRamp builds a PCM byte stream whose every byte is distinct from its
// neighbours, so a one-byte shift in the framing cannot go unnoticed.
func pcmRamp(sampleCount int) []byte {
	data := make([]byte, sampleCount*2)
	for i := range sampleCount {
		v := int16((i*2731)%40000 - 20000) //nolint:gosec // G115: intentional wrap for a synthetic test signal
		if i%2 == 1 {
			v = -v
		}
		data[i*2] = byte(v)        //nolint:gosec // G115: intentional int16 to byte truncation for PCM serialisation
		data[i*2+1] = byte(v >> 8) //nolint:gosec // G115: intentional int16 to byte truncation for PCM serialisation
	}
	return data
}

// alignTestStream builds a Stream from the package's own test config, overriding
// only the fields that determine the PCM frame size.
func alignTestStream(t *testing.T, bitDepth, channels, sourceChannels int, channelMode string, bufMgr *buffer.Manager) *Stream {
	t.Helper()
	cfg := newTestConfig()
	cfg.BitDepth = bitDepth
	cfg.Channels = channels
	cfg.SourceChannels = sourceChannels
	cfg.ChannelMode = channelMode
	return NewStream(&cfg, nil, nil, nil, bufMgr)
}

// collectFrames drains readCh until the reader reports an error, returning the
// concatenated payload. Each receive is bounded: readStdout never closes readCh,
// so an unbounded range would deadlock until the package timeout and take the
// whole package down with a goroutine dump instead of a named failure.
//
// releaseRefs makes the collector return each pooled buffer immediately, which
// lets the pool recycle it before the next read and so exercises the carry
// against a reused slice.
func collectFrames(t *testing.T, readCh <-chan readResult, frameBytes int, releaseRefs bool) ([]byte, error) {
	t.Helper()
	var got []byte
	for {
		select {
		case res := <-readCh:
			if res.err != nil {
				return got, res.err
			}
			assert.Zero(t, len(res.data)%frameBytes,
				"every frame handed downstream must hold whole PCM frames, got %d bytes for a %d-byte frame",
				len(res.data), frameBytes)
			got = append(got, res.data...)
			if releaseRefs {
				res.ref.Release()
			}
		case <-time.After(5 * time.Second):
			t.Fatal("readStdout produced no further result within 5s")
			return got, nil
		}
	}
}

// TestReadStdout_EmitsWholeFrames verifies that the FFmpeg reader never hands a
// partial PCM frame downstream, and never loses or reorders a byte doing so.
//
// A pipe read boundary is not a frame boundary, so a read that ends mid-frame is
// normal. Every downstream consumer (the router's alignment, EQ, gain and
// resampling paths, the sound level and audio level consumers, the analysis
// buffers) reinterprets these bytes as samples, and none of them can recover
// locally: dropping the remainder shifts every following byte, so each subsequent
// sample decodes with its halves swapped and turns into noise, while the
// resampler rejects the frame outright. Reassembly belongs at the producer.
//
// The cases cover every output format buildOutputArgs can select, because the
// frame size is derived from the configured bit depth and channel count. Mono
// PCM16 alone would pass even if the derivation were wrong, since its frame size
// happens to equal a single sample.
func TestReadStdout_EmitsWholeFrames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		bitDepth       int
		channels       int
		sourceChannels int
		channelMode    string
		frameBytes     int
	}{
		{name: "mono_s16le", bitDepth: 16, channels: 1, frameBytes: 2},
		// SourceChannels is left at zero so the pan path does not apply and
		// FFmpeg really is told to emit two channels.
		{name: "stereo_s16le", bitDepth: 16, channels: 2, frameBytes: 4},
		{name: "mono_s24le", bitDepth: 24, channels: 1, frameBytes: 3},
		{name: "stereo_s32le", bitDepth: 32, channels: 2, frameBytes: 8},
		{name: "unsupported_depth_falls_back_to_s16le", bitDepth: 20, channels: 1, frameBytes: 2},
		// The pan filter folds a multi-channel source to mono regardless of the
		// configured channel count, so the real frame is one sample wide.
		{name: "left_channel_mode_emits_mono", bitDepth: 16, channels: 2, sourceChannels: 2, channelMode: "left", frameBytes: 2},
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

			s := alignTestStream(t, tc.bitDepth, tc.channels, tc.sourceChannels, tc.channelMode, nil)
			require.Equal(t, tc.frameBytes, pcmFrameBytes(&s.config),
				"frame size must follow the output format buildOutputArgs selects")

			readCh := make(chan readResult, 64)
			readerDone := make(chan struct{})
			t.Cleanup(func() { close(readerDone) })

			reader := newScriptedReader(t, signal, script)
			go s.readStdout(reader, readCh, readerDone)

			got, err := collectFrames(t, readCh, tc.frameBytes, false)
			require.ErrorIs(t, err, io.EOF, "the scripted reader only ends with EOF")
			assert.Equal(t, signal, got,
				"the reassembled stream must match the source byte for byte; a mismatch means a byte was dropped or reordered")
			assert.Positive(t, s.partialFrameCarries.Load(), "this script must exercise the carry")
		})
	}
}

// TestReadStdout_PooledCarrySurvivesBufferRecycling drives the carry against a
// live buffer pool, releasing each frame as soon as it is collected so the pool
// can hand the same slice back on the next read. Without a pool the read buffer
// is a fresh allocation every iteration, so the pooled path is otherwise only
// covered by a test that reads four aligned bytes and never carries anything.
//
// The channel is unbuffered so the reader cannot borrow the next buffer until
// this collector has taken the previous frame, and the pool's hit count is
// asserted afterwards: recycling is the premise of the test, so a run where it
// never happened must fail rather than pass vacuously.
func TestReadStdout_PooledCarrySurvivesBufferRecycling(t *testing.T) {
	t.Parallel()

	signal := pcmRamp(4096)
	mgr := buffer.NewManager(audiocore.GetLogger())
	s := alignTestStream(t, 16, 1, 0, "", mgr)

	readCh := make(chan readResult)
	readerDone := make(chan struct{})
	t.Cleanup(func() { close(readerDone) })

	reader := newScriptedReader(t, signal, []int{1001, 999, 3, 1, 2047, 4095})
	go s.readStdout(reader, readCh, readerDone)

	got, err := collectFrames(t, readCh, 2, true)
	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, signal, got,
		"the reassembled stream must survive the read buffer being recycled between reads")
	assert.Positive(t, s.partialFrameCarries.Load(), "this script must exercise the carry")
	assert.Positive(t, mgr.BytePoolFor(ffmpegBufferSize).GetStats().Hits,
		"the pool must actually have recycled a buffer, otherwise this test proves nothing about recycling")
}

// TestReadStdout_DropsOnlyTheFinalPartialFrame verifies the one case where bytes
// legitimately do not come out: a stream that ends mid-frame has nothing left to
// complete it with, so the remainder is discarded at EOF rather than held forever.
func TestReadStdout_DropsOnlyTheFinalPartialFrame(t *testing.T) {
	t.Parallel()

	signal := append(pcmRamp(512), 0x7f) // odd total: the last byte has no pair
	s := alignTestStream(t, 16, 1, 0, "", nil)

	readCh := make(chan readResult, 64)
	readerDone := make(chan struct{})
	t.Cleanup(func() { close(readerDone) })

	reader := newScriptedReader(t, signal, []int{255, 513})
	go s.readStdout(reader, readCh, readerDone)

	got, err := collectFrames(t, readCh, 2, false)
	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, signal[:len(signal)-1], got,
		"everything but the unpaired trailing byte must be emitted")
}

// TestReadStdout_PendingCarryDroppedOnReadError checks that a read failure while
// a partial frame is pending forwards the error and does not emit the orphaned
// bytes as if they were a frame.
func TestReadStdout_PendingCarryDroppedOnReadError(t *testing.T) {
	t.Parallel()

	signal := append(pcmRamp(256), 0x7f) // ends mid-frame
	s := alignTestStream(t, 16, 1, 0, "", nil)

	reader := newScriptedReader(t, signal, []int{513})
	reader.failAtEnd = true

	readCh := make(chan readResult, 64)
	readerDone := make(chan struct{})
	t.Cleanup(func() { close(readerDone) })

	go s.readStdout(reader, readCh, readerDone)

	got, err := collectFrames(t, readCh, 2, false)
	require.ErrorIs(t, err, errScriptedFailure, "the read error must be forwarded")
	assert.Equal(t, signal[:len(signal)-1], got,
		"the pending partial frame must not be emitted when the stream fails")
	// Exactly one: the single read that delivered the orphan byte. The failing
	// read that follows delivers nothing new, so counting it again would report
	// two carries for one orphan and overstate the rate in the health log.
	assert.Equal(t, int64(1), s.partialFrameCarries.Load(),
		"one orphaned byte must be counted once, not once per read that observes it")
}

// TestPCMFrameBytes_Bounds pins the frame-size derivation at its edges. The
// oversize and huge-channel-count rows matter because the ceiling is applied to
// the multiplicand: applying it to the product instead would let a 32-bit build
// wrap to zero and divide by zero in readStdout.
func TestPCMFrameBytes_Bounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		bitDepth int
		channels int
		want     int
	}{
		{name: "mono_16", bitDepth: 16, channels: 1, want: 2},
		{name: "mono_24", bitDepth: 24, channels: 1, want: 3},
		{name: "stereo_32", bitDepth: 32, channels: 2, want: 8},
		{name: "zero_channels_treated_as_mono", bitDepth: 16, channels: 0, want: 2},
		{name: "negative_channels_treated_as_mono", bitDepth: 16, channels: -3, want: 2},
		{name: "frame_larger_than_read_buffer_falls_back", bitDepth: 32, channels: 20000, want: 1},
		{name: "channel_count_that_would_overflow_falls_back", bitDepth: 16, channels: 1 << 30, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := newTestConfig()
			cfg.BitDepth = tc.bitDepth
			cfg.Channels = tc.channels
			got := pcmFrameBytes(&cfg)
			assert.Equal(t, tc.want, got)
			assert.Positive(t, got, "the result must always be usable as a modulus")
			assert.LessOrEqual(t, got, ffmpegBufferSize, "a frame must fit in the read buffer")
		})
	}
}

// TestPCMSampleBytesMatchesFFmpegFormat pins the byte-size helper to the format
// string GetFFmpegFormat emits, so adding a bit depth to one without the other
// fails here instead of silently making stream readers frame on the wrong unit.
func TestPCMSampleBytesMatchesFFmpegFormat(t *testing.T) {
	t.Parallel()

	for _, bitDepth := range []int{0, 8, 16, 20, 24, 32, 64} {
		t.Run(strconv.Itoa(bitDepth), func(t *testing.T) {
			t.Parallel()
			_, _, format := GetFFmpegFormat(48000, 1, bitDepth)
			// Formats are named sNNle where NN is the bit count.
			require.Regexp(t, `^s\d+le$`, format)
			bits, err := strconv.Atoi(format[1 : len(format)-2])
			require.NoError(t, err)
			assert.Equal(t, bits/8, pcmSampleBytes(bitDepth),
				"sample size must match the %q format GetFFmpegFormat selects", format)
		})
	}
}

// TestPCMFrameBytes_MatchesFFmpegArgs pins the frame size to the channel count
// FFmpeg is actually told to emit. appendChannelArgs overrides the configured
// count with mono whenever the pan filter is used, so deriving the frame size
// from cfg.Channels alone would overstate it for those configurations.
func TestPCMFrameBytes_MatchesFFmpegArgs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		bitDepth       int
		channels       int
		sourceChannels int
		channelMode    string
	}{
		{name: "mono_downmix", bitDepth: 16, channels: 1, sourceChannels: 1, channelMode: "downmix"},
		{name: "stereo_empty_mode", bitDepth: 16, channels: 2, sourceChannels: 2, channelMode: ""},
		{name: "stereo_left", bitDepth: 16, channels: 2, sourceChannels: 2, channelMode: "left"},
		{name: "stereo_right", bitDepth: 16, channels: 2, sourceChannels: 2, channelMode: "right"},
		{name: "stereo_left_mono_source", bitDepth: 16, channels: 2, sourceChannels: 1, channelMode: "left"},
		{name: "s24_stereo_right", bitDepth: 24, channels: 2, sourceChannels: 4, channelMode: "RIGHT"},
		{name: "s32_quad_downmix", bitDepth: 32, channels: 4, sourceChannels: 4, channelMode: "downmix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := newTestConfig()
			cfg.BitDepth = tc.bitDepth
			cfg.Channels = tc.channels
			cfg.SourceChannels = tc.sourceChannels
			cfg.ChannelMode = tc.channelMode

			args := appendChannelArgs(nil, cfg.ChannelMode, cfg.SourceChannels, strconv.Itoa(cfg.Channels))
			acValue := 0
			for i, a := range args {
				if a == "-ac" && i+1 < len(args) {
					v, err := strconv.Atoi(args[i+1])
					require.NoError(t, err)
					acValue = v
				}
			}
			require.Positive(t, acValue, "appendChannelArgs must always emit an -ac value")

			assert.Equal(t, pcmSampleBytes(tc.bitDepth)*acValue, pcmFrameBytes(&cfg),
				"the frame size must match the channel count FFmpeg is told to emit")
		})
	}
}

// FuzzReadStdoutFraming asserts the framer's total invariants over arbitrary read
// sizes, which the hand-picked scripts above cannot cover: every frame handed
// downstream holds whole PCM frames, and the concatenated output is exactly the
// input truncated to a whole number of frames, with nothing reordered.
func FuzzReadStdoutFraming(f *testing.F) {
	f.Add(pcmRamp(200), []byte{99, 1, 200, 3}, uint8(0))
	f.Add(pcmRamp(64), []byte{1}, uint8(3))
	f.Add([]byte{1, 2, 3}, []byte{2}, uint8(1))

	formats := []struct{ bitDepth, channels int }{
		{16, 1}, {16, 2}, {24, 1}, {32, 2},
	}

	f.Fuzz(func(t *testing.T, payload, sizes []byte, format uint8) {
		fm := formats[int(format)%len(formats)]
		cfg := newTestConfig()
		cfg.BitDepth = fm.bitDepth
		cfg.Channels = fm.channels
		s := NewStream(&cfg, nil, nil, nil, nil)
		frameBytes := pcmFrameBytes(&cfg)

		script := make([]int, 0, len(sizes))
		for _, b := range sizes {
			if b > 0 {
				script = append(script, int(b))
			}
		}

		readCh := make(chan readResult, 64)
		readerDone := make(chan struct{})
		defer close(readerDone)

		go s.readStdout(&scriptedReader{data: payload, script: script}, readCh, readerDone)

		var got []byte
		for done := false; !done; {
			select {
			case res := <-readCh:
				if res.err != nil {
					done = true
					break
				}
				require.Zero(t, len(res.data)%frameBytes, "every emitted chunk must hold whole frames")
				got = append(got, res.data...)
			case <-time.After(10 * time.Second):
				t.Fatal("readStdout stalled")
			}
		}

		want := payload[:len(payload)-len(payload)%frameBytes]
		// bytes.Equal rather than require.Equal: an all-truncated payload leaves
		// got nil and want an empty slice, which are equal here but not to
		// reflect.DeepEqual.
		require.Truef(t, bytes.Equal(want, got),
			"the reassembled stream must equal the input truncated to whole frames (want %d bytes, got %d)",
			len(want), len(got))
	})
}
