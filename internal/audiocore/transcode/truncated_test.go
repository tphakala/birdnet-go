package transcode

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
)

const (
	// truncatedWAVFrames is the frame count a truncation fixture header declares.
	truncatedWAVFrames = 4000
	// wavFixtureHeaderBytes is the size of the canonical 16-bit PCM WAV header
	// wavBytes writes.
	wavFixtureHeaderBytes = 44
	// flacSyncHigh and flacSyncLow are the first two bytes of a FLAC frame header
	// with a fixed block size (the sync code 0xFFF8).
	flacSyncHigh = 0xFF
	flacSyncLow  = 0xF8
	// flacStreamHeaderBytes skips the fLaC marker and the STREAMINFO block so a
	// scan for the sync code cannot match inside the stream header.
	flacStreamHeaderBytes = 42
)

// truncatedWAV writes a WAV whose header declares truncatedWAVFrames frames but
// whose data chunk holds only keepFrames of them.
func truncatedWAV(t *testing.T, keepFrames int) string {
	t.Helper()
	data := wavBytes(t, testRate, 1, truncatedWAVFrames)
	path := filepath.Join(t.TempDir(), "cut.wav")
	require.NoError(t, os.WriteFile(path, data[:wavFixtureHeaderBytes+keepFrames*bytesPerSample], 0o600))
	return path
}

func TestDecodeMono_TruncatedWAVIsReported(t *testing.T) {
	t.Parallel()

	path := truncatedWAV(t, truncatedWAVFrames/2)

	_, err := DecodeMono(t.Context(), path, 0)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTruncatedSource)
	require.ErrorIs(t, err, ErrUndecodableSource, "a truncated source is also undecodable")

	pcm, err := decode(t.Context(), path, nil, 0)
	require.NoError(t, err, "the transcode paths keep rendering what exists")
	assert.Equal(t, truncatedWAVFrames/2, pcm.Frames())
}

func TestDecode_TruncatedFLACIsTruncatedSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "good.flac")
	writeEncoded(t, audioformat.FLAC, 0, good, tone(testRate, 1, 1, testToneHz, testAmp), testRate)
	goodBytes := readFile(t, good)

	// Locate the first two audio frames by scanning for the sync code. A cut
	// inside the metadata is a different failure (go-flac reports it as a plain
	// unexpected EOF), so both cuts below fall in the audio.
	syncCode := []byte{flacSyncHigh, flacSyncLow}
	first := flacStreamHeaderBytes + bytes.Index(goodBytes[flacStreamHeaderBytes:], syncCode)
	require.GreaterOrEqual(t, first, flacStreamHeaderBytes, "fixture holds a frame header")
	second := first + 2 + bytes.Index(goodBytes[first+2:], syncCode)
	require.Greater(t, second, first+2, "fixture holds a second frame")

	tests := []struct {
		name string
		data []byte
	}{
		{name: "mid frame", data: goodBytes[:(first+second)/2]},
		{name: "on a frame boundary", data: goodBytes[:second]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "cut.flac")
			require.NoError(t, os.WriteFile(path, tt.data, 0o600))

			_, err := decode(t.Context(), path, nil, 0)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrTruncatedSource)
			require.ErrorIs(t, err, ErrUndecodableSource)
		})
	}
}

func TestDecodeMono_CompleteSourcesAreNotTruncated(t *testing.T) {
	t.Parallel()

	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			path, _ := writeFixture(t, fx, testRate, testSeconds)
			_, err := DecodeMono(t.Context(), path, 0)
			require.NoError(t, err)
		})
	}

	for _, channels := range []int{1, 2} {
		path := filepath.Join(t.TempDir(), "whole.wav")
		require.NoError(t, os.WriteFile(path, wavBytes(t, testRate, channels, truncatedWAVFrames), 0o600))
		_, err := DecodeMono(t.Context(), path, 0)
		require.NoError(t, err, "%d channels", channels)
	}
}

func TestDecodeSource_DeclaredFramesOnlyForExactWholeReads(t *testing.T) {
	t.Parallel()

	const declared = 100
	const present = 40
	tests := []struct {
		name        string
		exactLength bool
		win         *window
		want        int64
	}{
		{name: "exact whole read", exactLength: true, want: declared},
		{name: "inexact whole read", exactLength: false, want: unknownFrames},
		{name: "exact windowed read", exactLength: true, win: &window{Start: 0, Duration: 0.001}, want: unknownFrames},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := &source{
				reader:      bytes.NewReader(make([]byte, present*bytesPerSample)),
				sampleRate:  testRate,
				channels:    1,
				totalFrames: declared,
				exactLength: tt.exactLength,
				expansion:   losslessExpansion,
			}
			pcm, err := decodeSource(t.Context(), src, tt.win, present, maxDecodedPCMBytes)
			require.NoError(t, err)
			assert.Equal(t, tt.want, pcm.declaredFrames)
		})
	}
}

func TestDecodedBytesBound(t *testing.T) {
	t.Parallel()

	const frameBytes = 2
	maxFrames := int64(maxDecodedPCMBytes / frameBytes)
	tests := []struct {
		name        string
		totalFrames int64
		want        int64
	}{
		{name: "known length", totalFrames: 1000, want: 1000 * frameBytes},
		{name: "above the cap is the cap plus one frame", totalFrames: maxFrames + 5, want: (maxFrames + 1) * frameBytes},
		{name: "huge declared length does not overflow", totalFrames: 1 << 62, want: (maxFrames + 1) * frameBytes},
		{name: "unknown length is the cap plus one frame", totalFrames: unknownFrames, want: maxDecodedPCMBytes + frameBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, decodedBytesBound(tt.totalFrames, frameBytes))
		})
	}
}

func TestProbeFile_DecodedBytesBound(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "known.wav")
	require.NoError(t, os.WriteFile(path, wavBytes(t, testRate, 1, truncatedWAVFrames), 0o600))
	info, err := ProbeFile(t.Context(), path)
	require.NoError(t, err)
	assert.Equal(t, int64(truncatedWAVFrames*bytesPerSample), info.DecodedBytes)

	mp3Path, _ := writeFixture(t, fixtureNamed(t, "mp3"), testRate, 1)
	info, err = ProbeFile(t.Context(), mp3Path)
	require.NoError(t, err)
	assert.Positive(t, info.DecodedBytes)
	assert.LessOrEqual(t, info.DecodedBytes, int64(maxDecodedPCMBytes+info.Channels*bytesPerSample))
}

const (
	// wav24BitDepth is the bit depth of the 24-bit fixtures.
	wav24BitDepth = 24
	// bitsPerByte converts a bit depth to bytes per sample.
	bitsPerByte = 8
	// wavSizeFieldBytes is the width of a WAV header size field.
	wavSizeFieldBytes = 4
	// stereoChannels is the channel count of the stereo fixture.
	stereoChannels = 2
)

// wav24Bytes returns a mono 24-bit WAV of n zero frames at testRate.
func wav24Bytes(t *testing.T, frames int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wavpcm.EncodeInterleaved(&buf, wavpcm.Config{SampleRate: testRate, BitDepth: wav24BitDepth, Channels: 1}, make([]byte, frames*wav24BitDepth/bitsPerByte)))
	return buf.Bytes()
}

func TestDecodeMono_TruncationCheckAcrossWAVLayouts(t *testing.T) {
	t.Parallel()

	const frames = truncatedWAVFrames
	whole24 := wav24Bytes(t, frames)
	stereo := wavBytes(t, testRate, stereoChannels, frames)
	streaming := wavBytes(t, testRate, 1, frames)
	for _, off := range []int{wavRIFFSizeOffset, wavDataSizeOffset} {
		copy(streaming[off:off+wavSizeFieldBytes], make([]byte, wavSizeFieldBytes)) // a zero size field marks a streamed WAV
	}
	tests := []struct {
		name      string
		data      []byte
		wantTrunc bool
	}{
		{name: "complete 24-bit", data: whole24},
		{name: "truncated 24-bit", data: whole24[:wavFixtureHeaderBytes+frames/2*wav24BitDepth/bitsPerByte], wantTrunc: true},
		{name: "truncated stereo", data: stereo[:wavFixtureHeaderBytes+frames/2*stereoChannels*bytesPerSample], wantTrunc: true},
		{name: "streamed WAV has no declared length to compare", data: streaming[:wavFixtureHeaderBytes+frames/2*bytesPerSample]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "layout.wav")
			require.NoError(t, os.WriteFile(path, tt.data, 0o600))

			_, err := DecodeMono(t.Context(), path, 0)
			if tt.wantTrunc {
				require.ErrorIs(t, err, ErrTruncatedSource)
				return
			}
			require.NoError(t, err)
		})
	}
}
