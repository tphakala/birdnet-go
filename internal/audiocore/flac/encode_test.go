package flac

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goflac "github.com/tphakala/go-flac/pcm"
)

const (
	testSampleRate = 48000
	testChannels   = 1
	testBitDepth   = bitDepth16
)

// makeTestPCM builds deterministic interleaved int16 LE mono PCM with n samples,
// mixing two tones so the encoder exercises real predictors rather than silence.
func makeTestPCM(n int) []byte {
	samples := make([]int16, n)
	for i := range samples {
		v := 8000.0*math.Sin(2*math.Pi*440*float64(i)/testSampleRate) +
			3000.0*math.Sin(2*math.Pi*1200*float64(i)/testSampleRate)
		samples[i] = int16(v)
	}
	return pcm16(samples...)
}

// decodeFLAC reads a FLAC file back to interleaved little-endian PCM bytes.
func decodeFLAC(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	dec, err := goflac.NewDecoder(f)
	require.NoError(t, err)
	out, err := io.ReadAll(dec)
	require.NoError(t, err)
	return out
}

func baseOpts(path string, pcm []byte) *Options {
	return &Options{
		PCMData:    pcm,
		OutputPath: path,
		SampleRate: testSampleRate,
		Channels:   testChannels,
		BitDepth:   testBitDepth,
	}
}

func TestEncodePCM_RoundTripLossless(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(10000)
	path := filepath.Join(t.TempDir(), "clip.flac")
	require.NoError(t, EncodePCM(t.Context(), baseOpts(path, pcm)))
	assert.Equal(t, pcm, decodeFLAC(t, path), "decoded PCM must equal input (lossless)")
}

// assertNoTempLeftover fails if any temp file remains in the output directory.
// It replaces the older exact-path checks (outputPath + ".temp"): the temp file
// now carries a per-export unique token before the suffix, so cleanup is
// verified by globbing the directory rather than by a fixed name.
func assertNoTempLeftover(t *testing.T, outputPath string) {
	t.Helper()
	leftover, err := filepath.Glob(filepath.Join(filepath.Dir(outputPath), "*"+tempExt))
	require.NoError(t, err)
	assert.Empty(t, leftover, "no %s files should remain in %s", tempExt, filepath.Dir(outputPath))
}

// TestEncodePCM_ConcurrentSamePathNoTempCollision reproduces GitHub #3323: when
// several exports target the same OutputPath (e.g. two audio sources detect the
// same species in the same one-second window at the same rounded confidence),
// each must encode to its own temp file. Previously every export wrote to the
// shared OutputPath+tempExt and renamed it into place, so the first rename won
// and the rest failed with ENOENT ("no such file or directory"), permanently
// dropping those clips (and the shared temp could be corrupted by concurrent
// writers). All exports must succeed and leave a valid lossless clip behind.
func TestEncodePCM_ConcurrentSamePathNoTempCollision(t *testing.T) {
	t.Parallel()
	const workers = 32
	dir := t.TempDir()
	path := filepath.Join(dir, "columba_palumbus_95p_20260531T083828Z.flac")
	pcm := makeTestPCM(20000)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release all goroutines together to maximise collision
			errs[i] = EncodePCM(t.Context(), baseOpts(path, pcm))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "concurrent export %d must not fail on a shared temp path", i)
	}
	// Every worker encodes identical PCM, so whichever export wins the last
	// rename (last-writer-wins dedup) the surviving clip decodes to the same
	// bytes; this assertion is deterministic only because the inputs are equal.
	assert.Equal(t, pcm, decodeFLAC(t, path), "the surviving clip must be a valid lossless FLAC")
	assertNoTempLeftover(t, path)
}

func TestEncodePCM_GainRoundTrip(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(10000)
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), pcm)
	opts.GainDB = 6.0 // roughly 2x
	require.NoError(t, EncodePCM(t.Context(), opts))

	want := make([]byte, len(pcm))
	applyGainInt16(want, pcm, math.Pow(10, opts.GainDB/20))
	assert.Equal(t, want, decodeFLAC(t, opts.OutputPath),
		"decoded PCM must equal the gained input (gain applied, stored losslessly)")
}

// TestEncodePCM_GainRoundTripMultiChunk drives PCM larger than two gain scratch
// chunks with a non-aligned final chunk, so the gain loop iterates several times
// including a short final chunk (the single-chunk GainRoundTrip test cannot
// reach those branches).
func TestEncodePCM_GainRoundTripMultiChunk(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(2*gainScratchBytes + 777)
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), pcm)
	opts.GainDB = 6.0
	require.NoError(t, EncodePCM(t.Context(), opts))

	want := make([]byte, len(pcm))
	applyGainInt16(want, pcm, math.Pow(10, opts.GainDB/20))
	assert.Equal(t, want, decodeFLAC(t, opts.OutputPath))
}

// makeTestPCMStereo builds interleaved L/R int16 LE PCM with the given frame
// count (two tones, distinct per channel).
func makeTestPCMStereo(frames int) []byte {
	samples := make([]int16, frames*2)
	for i := range frames {
		samples[2*i] = int16(8000.0 * math.Sin(2*math.Pi*440*float64(i)/testSampleRate))
		samples[2*i+1] = int16(6000.0 * math.Sin(2*math.Pi*660*float64(i)/testSampleRate))
	}
	return pcm16(samples...)
}

func TestEncodePCM_StereoRoundTrip(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCMStereo(10000)
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), pcm)
	opts.Channels = 2
	require.NoError(t, EncodePCM(t.Context(), opts))
	assert.Equal(t, pcm, decodeFLAC(t, opts.OutputPath), "stereo decode must be lossless")
}

// TestEncodePCM_BlockSizeBoundaries covers FLAC framing edges around the 4096
// sample block size: below, exactly, and just over a block.
func TestEncodePCM_BlockSizeBoundaries(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 4095, 4096, 4097} {
		t.Run(fmt.Sprintf("%d_samples", n), func(t *testing.T) {
			t.Parallel()
			pcm := makeTestPCM(n)
			path := filepath.Join(t.TempDir(), "clip.flac")
			require.NoError(t, EncodePCM(t.Context(), baseOpts(path, pcm)))
			assert.Equal(t, pcm, decodeFLAC(t, path))
		})
	}
}

func TestEncodePCM_EmptyPCMReturnsError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clip.flac")
	err := EncodePCM(t.Context(), baseOpts(path, nil))
	require.Error(t, err)
	assert.NoFileExists(t, path)
}

func TestEncodePCM_UnsupportedBitDepthReturnsError(t *testing.T) {
	t.Parallel()
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), makeTestPCM(100))
	opts.BitDepth = 24
	err := EncodePCM(t.Context(), opts)
	require.Error(t, err)
	assert.NoFileExists(t, opts.OutputPath)
}

// TestEncodePCM_CancelledBeforeStart hits the entry guard: a context already
// cancelled when EncodePCM is called returns before any file is created.
func TestEncodePCM_CancelledBeforeStart(t *testing.T) {
	t.Parallel()
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), makeTestPCM(10000))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := EncodePCM(ctx, opts)
	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, opts.OutputPath)
	assertNoTempLeftover(t, opts.OutputPath)
}

// errAfterCtx returns nil from Err() for the first `after` calls, then
// context.Canceled, letting a test drive cancellation to a precise point. The
// entry guard consumes one Err() call, so after=1 cancels at the first gain-loop
// iteration (after the temp file has been created).
type errAfterCtx struct {
	context.Context
	mu    sync.Mutex
	calls int
	after int
}

func (c *errAfterCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls > c.after {
		return context.Canceled
	}
	return nil
}

// TestEncodePCM_CancelledDuringEncode exercises the per-chunk ctx check inside
// the gain loop (not the entry guard): the temp file is created, then the loop's
// cancellation check fires and the temp file is cleaned up.
func TestEncodePCM_CancelledDuringEncode(t *testing.T) {
	t.Parallel()
	opts := baseOpts(filepath.Join(t.TempDir(), "clip.flac"), makeTestPCM(10000))
	opts.GainDB = 3.0 // gain path runs the per-chunk ctx check
	ctx := &errAfterCtx{Context: t.Context(), after: 1}
	err := EncodePCM(ctx, opts)
	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, opts.OutputPath)
	assertNoTempLeftover(t, opts.OutputPath)
}

// TestEncoderReuseAfterAbortedWrite verifies the pooling safety contract that
// EncodePCM relies on: an encoder abandoned mid-stream WITHOUT Close (the state
// it is in when an aborted EncodePCM returns it to the pool) produces a correct,
// lossless file on its next use, because Reset fully clears the leftover stream
// state. This guards against a go-flac regression that would otherwise leak
// partial-write state into the next pooled encode (raised by Sentry on #3483).
func TestEncoderReuseAfterAbortedWrite(t *testing.T) {
	t.Parallel()
	cfg := goflac.Config{
		SampleRate:        testSampleRate,
		Channels:          testChannels,
		BitDepth:          testBitDepth,
		CompressionLevel:  defaultCompressionLevel,
		SeekTableInterval: testSampleRate,
	}
	dir := t.TempDir()

	// First stream: write a partial block, then abandon WITHOUT enc.Close(),
	// leaving the encoder "dirty" exactly as an aborted EncodePCM would.
	enc := new(goflac.Encoder)
	f1, err := os.Create(filepath.Join(dir, "aborted.flac")) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.NoError(t, enc.Reset(f1, cfg))
	_, err = enc.Write(makeTestPCM(1000))
	require.NoError(t, err)
	require.NoError(t, f1.Close()) // file closed; encoder deliberately not closed

	// Reuse the same encoder for a full stream: Reset must discard the leftover
	// state from the aborted stream and yield a byte-for-byte lossless result.
	pcm := makeTestPCM(12000)
	path := filepath.Join(dir, "ok.flac")
	f2, err := os.Create(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.NoError(t, enc.Reset(f2, cfg))
	_, err = enc.Write(pcm)
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	require.NoError(t, f2.Close())

	assert.Equal(t, pcm, decodeFLAC(t, path),
		"encoder reused after an aborted write must produce a lossless file")
}

// TestEncoderReuseAfterClose covers the common pooling path: a fully encoded and
// Closed encoder (what EncodePCM puts back after a successful export) is reused
// for a new stream via Reset. This is the path executed on every successful
// native export, so it must reinitialize cleanly and stay lossless (raised by
// Sentry on #3483; complements TestEncoderReuseAfterAbortedWrite, which covers
// the un-Closed/aborted state).
func TestEncoderReuseAfterClose(t *testing.T) {
	t.Parallel()
	cfg := goflac.Config{
		SampleRate:        testSampleRate,
		Channels:          testChannels,
		BitDepth:          testBitDepth,
		CompressionLevel:  defaultCompressionLevel,
		SeekTableInterval: testSampleRate,
	}
	dir := t.TempDir()
	enc := new(goflac.Encoder)

	// First stream: full encode AND Close (the normal success path).
	first := makeTestPCM(8000)
	firstPath := filepath.Join(dir, "first.flac")
	f1, err := os.Create(firstPath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.NoError(t, enc.Reset(f1, cfg))
	_, err = enc.Write(first)
	require.NoError(t, err)
	require.NoError(t, enc.Close()) // encoder is now closed
	require.NoError(t, f1.Close())
	assert.Equal(t, first, decodeFLAC(t, firstPath))

	// Reuse the CLOSED encoder for a second stream: Reset must reinitialize it.
	second := makeTestPCM(12000)
	secondPath := filepath.Join(dir, "second.flac")
	f2, err := os.Create(secondPath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	require.NoError(t, enc.Reset(f2, cfg))
	_, err = enc.Write(second)
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	require.NoError(t, f2.Close())
	assert.Equal(t, second, decodeFLAC(t, secondPath),
		"encoder reused after Close must produce a lossless file")
}

func TestEncodePCM_NoTempFileLeftOnSuccess(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clip.flac")
	require.NoError(t, EncodePCM(t.Context(), baseOpts(path, makeTestPCM(5000))))
	assert.FileExists(t, path)
	assertNoTempLeftover(t, path)
}

func TestEncodePCM_EmitsSeekTable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clip.flac")
	require.NoError(t, EncodePCM(t.Context(), baseOpts(path, makeTestPCM(60000))))
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.True(t, hasSeekTable(data), "native FLAC should emit a SEEKTABLE for player parity with FFmpeg")
}

// hasSeekTable walks the FLAC metadata blocks and reports whether a SEEKTABLE
// (block type 3) is present.
func hasSeekTable(data []byte) bool {
	const seekTableBlockType = 3
	if len(data) < 4 || string(data[:4]) != "fLaC" {
		return false
	}
	for off := 4; off+4 <= len(data); {
		header := data[off]
		last := header&0x80 != 0
		btype := header & 0x7f
		length := int(data[off+1])<<16 | int(data[off+2])<<8 | int(data[off+3])
		if btype == seekTableBlockType {
			return true
		}
		off += 4 + length
		if last {
			break
		}
	}
	return false
}

func TestSupportedBitDepth(t *testing.T) {
	t.Parallel()
	assert.True(t, SupportedBitDepth(16))
	assert.False(t, SupportedBitDepth(24))
	assert.False(t, SupportedBitDepth(0))
}

// decodeFLACBytes reads a FLAC byte stream back to interleaved little-endian PCM.
func decodeFLACBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	dec, err := goflac.NewDecoder(bytes.NewReader(data))
	require.NoError(t, err)
	out, err := io.ReadAll(dec)
	require.NoError(t, err)
	return out
}

func bufferBaseOpts(pcm []byte) *BufferOptions {
	return &BufferOptions{
		PCMData:    pcm,
		SampleRate: testSampleRate,
		Channels:   testChannels,
		BitDepth:   testBitDepth,
	}
}

func TestEncodePCMToBuffer_RoundTripLossless(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(10000)
	buf, err := EncodePCMToBuffer(t.Context(), bufferBaseOpts(pcm))
	require.NoError(t, err)
	require.NotNil(t, buf)
	assert.Equal(t, pcm, decodeFLACBytes(t, buf.Bytes()), "decoded PCM must equal input (lossless)")
}

// TestEncodePCMToBuffer_GainRoundTrip uses PCM larger than two gain scratch
// chunks (with a non-aligned tail) so the shared chunked gain loop is exercised
// through the buffer path too.
func TestEncodePCMToBuffer_GainRoundTrip(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(2*gainScratchBytes + 777)
	opts := bufferBaseOpts(pcm)
	opts.GainDB = 6.0 // roughly 2x
	buf, err := EncodePCMToBuffer(t.Context(), opts)
	require.NoError(t, err)

	want := make([]byte, len(pcm))
	applyGainInt16(want, pcm, math.Pow(10, opts.GainDB/20))
	assert.Equal(t, want, decodeFLACBytes(t, buf.Bytes()),
		"decoded PCM must equal the gained input (gain applied, stored losslessly)")
}

func TestEncodePCMToBuffer_StereoRoundTrip(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCMStereo(10000)
	opts := bufferBaseOpts(pcm)
	opts.Channels = 2
	buf, err := EncodePCMToBuffer(t.Context(), opts)
	require.NoError(t, err)
	assert.Equal(t, pcm, decodeFLACBytes(t, buf.Bytes()), "stereo decode must be lossless")
}

// TestEncodePCMToBuffer_NoSeekTable documents the intentional difference from the
// file path: a bytes.Buffer is not seekable, so no SEEKTABLE is emitted, yet the
// stream stays valid and decodable.
func TestEncodePCMToBuffer_NoSeekTable(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(60000)
	buf, err := EncodePCMToBuffer(t.Context(), bufferBaseOpts(pcm))
	require.NoError(t, err)
	assert.False(t, hasSeekTable(buf.Bytes()), "buffer path must not emit a SEEKTABLE (non-seekable sink)")
	assert.Equal(t, pcm, decodeFLACBytes(t, buf.Bytes()), "stream must still decode losslessly")
}

// TestEncodePCMToBuffer_FinalizesTotalSamples verifies the buffer path writes a
// finalized STREAMINFO.total_samples up front rather than the zero "unknown"
// sentinel, so a non-seekable sink still yields the correct sample count.
// Consumers like BirdWeather derive the soundscape duration from this field.
func TestEncodePCMToBuffer_FinalizesTotalSamples(t *testing.T) {
	t.Parallel()
	const sampleCount = 12345 // mono, so frames == samples
	pcm := makeTestPCM(sampleCount)
	buf, err := EncodePCMToBuffer(t.Context(), bufferBaseOpts(pcm))
	require.NoError(t, err)

	dec, err := goflac.NewDecoder(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	info := dec.Info()
	assert.NotZero(t, info.TotalSamples, "total_samples must be finalized, not the unknown sentinel")
	assert.Equal(t, uint64(sampleCount), info.TotalSamples,
		"total_samples must equal the input frame count")
}

// TestEncodePCMToBuffer_FinalizesBlockSize is the birdnet-go-side regression guard
// for GitHub #3965: the in-memory BirdWeather upload path (a non-seekable
// bytes.Buffer sink) must emit a STREAMINFO with a finalized, non-zero min/max block
// size. A zero max_blocksize makes strict decoders (browser Web Audio decodeAudioData,
// Apple CoreAudio) reject the soundscape, because they derive each fixed-blocksize
// frame's running sample number as frame_number * max_blocksize and a zero collapses
// every frame to sample 0. Requires go-flac >= v0.4.1.
func TestEncodePCMToBuffer_FinalizesBlockSize(t *testing.T) {
	t.Parallel()
	const flacBlockSize = 4096 // go-flac's fixed encoder block size
	// Several full 4096-sample frames plus a short final frame, matching a real clip.
	pcm := makeTestPCM(5*flacBlockSize + 777)
	buf, err := EncodePCMToBuffer(t.Context(), bufferBaseOpts(pcm))
	require.NoError(t, err)

	minBlk, maxBlk := streamInfoBlockSizes(t, buf.Bytes())
	assert.Equal(t, flacBlockSize, minBlk, "min_blocksize must be finalized, not the 0 unknown sentinel")
	assert.Equal(t, flacBlockSize, maxBlk, "max_blocksize must be finalized, not the 0 unknown sentinel")
}

// streamInfoBlockSizes parses the STREAMINFO min/max block size (in samples) from an
// in-memory FLAC stream. STREAMINFO is always the first metadata block, so its body
// starts at byte 8 ("fLaC" marker + 4-byte block header), with min_blocksize and
// max_blocksize as the first two big-endian uint16 fields (bytes 8..9 and 10..11).
func streamInfoBlockSizes(t *testing.T, flacBytes []byte) (minBlock, maxBlock int) {
	t.Helper()
	require.GreaterOrEqual(t, len(flacBytes), 12, "FLAC stream too short to hold STREAMINFO block sizes")
	require.Equal(t, "fLaC", string(flacBytes[:4]), "missing fLaC stream marker")
	return int(binary.BigEndian.Uint16(flacBytes[8:10])), int(binary.BigEndian.Uint16(flacBytes[10:12]))
}

func TestEncodePCMToBuffer_Validation(t *testing.T) {
	t.Parallel()
	t.Run("nil options", func(t *testing.T) {
		t.Parallel()
		_, err := EncodePCMToBuffer(t.Context(), nil)
		require.Error(t, err)
	})
	t.Run("empty pcm", func(t *testing.T) {
		t.Parallel()
		_, err := EncodePCMToBuffer(t.Context(), bufferBaseOpts(nil))
		require.Error(t, err)
	})
	t.Run("unsupported bit depth", func(t *testing.T) {
		t.Parallel()
		opts := bufferBaseOpts(makeTestPCM(100))
		opts.BitDepth = 24
		_, err := EncodePCMToBuffer(t.Context(), opts)
		require.Error(t, err)
	})
}

func TestEncodePCMToBuffer_CancelledBeforeStart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := EncodePCMToBuffer(ctx, bufferBaseOpts(makeTestPCM(10000)))
	require.ErrorIs(t, err, context.Canceled)
}

// TestEncodePCMToBuffer_CancelledDuringEncode exercises the per-chunk ctx check
// inside the shared gain loop via the buffer path (the entry guard consumes one
// Err() call, so after=1 cancels at the first gain-loop iteration).
func TestEncodePCMToBuffer_CancelledDuringEncode(t *testing.T) {
	t.Parallel()
	opts := bufferBaseOpts(makeTestPCM(2*gainScratchBytes + 777))
	opts.GainDB = 3.0 // gain path runs the per-chunk ctx check
	ctx := &errAfterCtx{Context: t.Context(), after: 1}
	_, err := EncodePCMToBuffer(ctx, opts)
	require.ErrorIs(t, err, context.Canceled)
}
