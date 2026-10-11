package flac

import (
	"bytes"
	"crypto/md5" //nolint:gosec // FLAC STREAMINFO uses MD5 as a content digest, not for security
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// streamInfoMD5Offset is the byte offset of the 16-byte STREAMINFO MD5:
	// 4 bytes "fLaC" magic + 4 bytes metadata block header + 18 bytes of
	// STREAMINFO fields that precede the MD5 (min/max block size 4, min/max
	// frame size 6, sample rate/channels/bit depth/total samples 8).
	streamInfoMD5Offset = 4 + 4 + 18
	streamInfoMD5Len    = 16
)

// streamInfoMD5 returns the STREAMINFO MD5 field of a FLAC stream.
func streamInfoMD5(t *testing.T, flacBytes []byte) []byte {
	t.Helper()
	require.GreaterOrEqual(t, len(flacBytes), streamInfoMD5Offset+streamInfoMD5Len)
	return flacBytes[streamInfoMD5Offset : streamInfoMD5Offset+streamInfoMD5Len]
}

// TestBufferEncodeConfig_SkipsMD5 pins that the buffer path asks go-flac to skip
// the MD5 pass. Byte identity alone holds with or without it, so this is the
// assertion that fails if SkipMD5 is dropped.
func TestBufferEncodeConfig_SkipsMD5(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(1000)
	cfg := bufferEncodeConfig(bufferBaseOpts(pcm), 1000)
	assert.True(t, cfg.SkipMD5)
}

func TestEncodePCMToBuffer_SkipMD5ByteIdentical(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pcm      []byte
		channels int
		gainDB   float64
	}{
		{"mono no gain", makeTestPCM(20000), 1, 0},
		{"mono multi-chunk gain", makeTestPCM(2*gainScratchBytes + 777), 1, 6.0},
		{"stereo", makeTestPCMStereo(20000), 2, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := bufferBaseOpts(tc.pcm)
			opts.Channels = tc.channels
			opts.GainDB = tc.gainDB

			got, err := EncodePCMToBuffer(t.Context(), opts)
			require.NoError(t, err)

			bytesPerFrame := (opts.BitDepth / 8) * opts.Channels
			cfg := bufferEncodeConfig(opts, uint64(len(opts.PCMData)/bytesPerFrame))
			cfg.SkipMD5 = false
			var want bytes.Buffer
			require.NoError(t, encodeToWriter(t.Context(), &want, &cfg, opts.PCMData, opts.GainDB))

			assert.Equal(t, want.Bytes(), got.Bytes(), "SkipMD5 must not change the output bytes")
			assert.Equal(t, make([]byte, streamInfoMD5Len), streamInfoMD5(t, got.Bytes()))
		})
	}
}

func TestEncodePCM_WritesRealMD5(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(20000)
	path := filepath.Join(t.TempDir(), "clip.flac")
	require.NoError(t, EncodePCM(t.Context(), baseOpts(path, pcm)))

	data, err := os.ReadFile(path) //nolint:gosec // test temp path
	require.NoError(t, err)
	sum := md5.Sum(pcm) //nolint:gosec // content digest, see import
	assert.Equal(t, sum[:], streamInfoMD5(t, data))
}

// TestEncoderPool_SkipMD5DoesNotLeakIntoFilePath alternates buffer and file
// exports so a pooled encoder that last served the buffer path is reused for a
// file export. Not parallel: it relies on the shared pool being exercised in
// sequence on one goroutine.
func TestEncoderPool_SkipMD5DoesNotLeakIntoFilePath(t *testing.T) {
	pcm := makeTestPCM(20000)
	sum := md5.Sum(pcm) //nolint:gosec // content digest, see import
	dir := t.TempDir()
	for i := range 4 {
		buf, err := EncodePCMToBuffer(t.Context(), bufferBaseOpts(pcm))
		require.NoError(t, err)
		assert.Equal(t, make([]byte, streamInfoMD5Len), streamInfoMD5(t, buf.Bytes()), "iteration %d buffer", i)

		path := filepath.Join(dir, "clip.flac")
		require.NoError(t, EncodePCM(t.Context(), baseOpts(path, pcm)))
		data, err := os.ReadFile(path) //nolint:gosec // test temp path
		require.NoError(t, err)
		assert.Equal(t, sum[:], streamInfoMD5(t, data), "iteration %d file", i)
		require.NoError(t, os.Remove(path))
	}
}

// BenchmarkEncodePCMToBuffer compares the buffer encode with and without the
// MD5 pass on 15 s of 48 kHz mono PCM.
func BenchmarkEncodePCMToBuffer(b *testing.B) {
	pcm := makeTestPCM(15 * testSampleRate)
	opts := bufferBaseOpts(pcm)
	for _, bc := range []struct {
		name    string
		skipMD5 bool
	}{{"hash", false}, {"skip", true}} {
		b.Run(bc.name, func(b *testing.B) {
			cfg := bufferEncodeConfig(opts, uint64(len(pcm)/2))
			cfg.SkipMD5 = bc.skipMD5
			b.SetBytes(int64(len(pcm)))
			b.ReportAllocs()
			for b.Loop() {
				var out bytes.Buffer
				if err := encodeToWriter(b.Context(), &out, &cfg, pcm, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
