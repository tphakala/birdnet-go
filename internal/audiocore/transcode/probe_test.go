package transcode

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// probeTestRate is the sample rate of the probe fixtures.
const probeTestRate = 48000

// probeTestSeconds is the length of the probe fixtures.
const probeTestSeconds = 1.0

// garbageProbeBytes is the size of the non-audio file written per extension.
const garbageProbeBytes = 16

// sentinelOf names which package sentinel err wraps, or "" for none.
func sentinelOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrUndecodableSource):
		return "undecodable"
	case errors.Is(err, ErrUnsupportedSource):
		return "unsupported"
	default:
		return ""
	}
}

func TestProbeFile_AllClipFormats(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			path, _ := writeFixture(t, fx, probeTestRate, probeTestSeconds)

			info, err := ProbeFile(t.Context(), path)
			require.NoError(t, err)
			mono, err := DecodeMono(t.Context(), path, 0)
			require.NoError(t, err)

			assert.Equal(t, 1, info.Channels)
			assert.Equal(t, mono.SampleRate(), info.SampleRate)
		})
	}
}

func TestProbeFile_ChannelBoundsMatchDecodeMono(t *testing.T) {
	stereo := filepath.Join(t.TempDir(), "stereo.wav")
	writeWAVChannels(t, stereo, make([]int16, 2*testRate), testRate, 2)
	info, err := ProbeFile(t.Context(), stereo)
	require.NoError(t, err)
	assert.Equal(t, 2, info.Channels)

	three := filepath.Join(t.TempDir(), "three.wav")
	writeWAVChannels(t, three, make([]int16, 3*100), testRate, 3)
	_, err = ProbeFile(t.Context(), three)
	require.ErrorIs(t, err, ErrUnsupportedSource)
}

func TestProbeFile_EmptyAndGarbage(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			dir := t.TempDir()
			empty := filepath.Join(dir, "empty"+fx.ext)
			writeBytes(t, empty, nil)
			garbage := filepath.Join(dir, "garbage"+fx.ext)
			writeBytes(t, garbage, []byte("0123456789abcdef")[:garbageProbeBytes])

			for _, p := range []string{empty, garbage} {
				_, err := ProbeFile(t.Context(), p)
				require.ErrorIs(t, err, ErrUndecodableSource, p)
			}
		})
	}
}

func TestProbeFile_HeaderOnlyWAVHoldsNoAudio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "header.wav")
	writeWAVChannels(t, path, nil, testRate, 1)

	_, probeErr := ProbeFile(t.Context(), path)
	_, decodeErr := DecodeMono(t.Context(), path, 0)

	require.ErrorIs(t, decodeErr, ErrUndecodableSource)
	require.ErrorIs(t, probeErr, ErrUndecodableSource)
}

func TestProbeFile_NeverStricterThanDecodeMono(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			src, _ := writeFixture(t, fx, probeTestRate, probeTestSeconds)
			data, err := os.ReadFile(src)
			require.NoError(t, err)

			for _, n := range []int{4, 12, 44, len(data) / 4, len(data) / 2, len(data) - 1} {
				cut := filepath.Join(t.TempDir(), "cut"+fx.ext)
				writeBytes(t, cut, data[:n])

				_, probeErr := ProbeFile(t.Context(), cut)
				_, decodeErr := DecodeMono(t.Context(), cut, 0)

				if probeErr != nil {
					assert.Equalf(t, sentinelOf(decodeErr), sentinelOf(probeErr), "cut at %d: probe %v, decode %v", n, probeErr, decodeErr)
					require.Errorf(t, decodeErr, "cut at %d: probe failed but decode succeeded", n)
				}
				if decodeErr == nil {
					assert.NoErrorf(t, probeErr, "cut at %d", n)
				}
			}
		})
	}
}

func TestProbeFile_UnsupportedExtensions(t *testing.T) {
	for _, name := range []string{"clip.aac", "clip.ogg", "clip.WAV"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			writeBytes(t, path, make([]byte, 2048))

			_, err := ProbeFile(t.Context(), path)

			require.ErrorIs(t, err, ErrUnsupportedSource)
		})
	}
}

func TestProbeFile_MissingAndCanceled(t *testing.T) {
	_, err := ProbeFile(t.Context(), filepath.Join(t.TempDir(), "missing.wav"))
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.NotErrorIs(t, err, ErrUndecodableSource)
	require.NotErrorIs(t, err, ErrUnsupportedSource)

	path, _ := writeFixture(t, fixtureNamed(t, "wav"), probeTestRate, probeTestSeconds)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = ProbeFile(ctx, path)
	require.ErrorIs(t, err, context.Canceled)
}

// openForProbe opens path read-only and closes it when the test ends.
func openForProbe(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: test fixture path
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// requireStatError asserts err is an enhanced error from the decode_stat step
// with the given category.
func requireStatError(t *testing.T, err error, category errors.ErrorCategory) {
	t.Helper()
	enhanced, ok := errors.AsType[*errors.EnhancedError](err)
	require.True(t, ok)
	assert.Equal(t, category, enhanced.Category)
	assert.Equal(t, "decode_stat", enhanced.GetContext()["operation"])
}

func TestProbeOpenFile_MatchesProbeFile(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			path, _ := writeFixture(t, fx, probeTestRate, probeTestSeconds)
			want, err := ProbeFile(t.Context(), path)
			require.NoError(t, err)

			f := openForProbe(t, path)
			got, err := ProbeOpenFile(t.Context(), f, path)
			require.NoError(t, err)
			assert.Equal(t, want, got)

			// The caller keeps ownership: the handle is still open.
			_, err = f.Stat()
			require.NoError(t, err)
		})
	}
}

func TestProbeOpenFile_ProbesTheOpenedInodeNotThePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an open file cannot be removed on Windows")
	}
	path := filepath.Join(t.TempDir(), "clip.wav")
	writeWAVChannels(t, path, make([]int16, 2*testRate), testRate, 2)
	f := openForProbe(t, path)

	// Replace the path with a different clip after the open.
	require.NoError(t, os.Remove(path))
	writeWAVChannels(t, path, make([]int16, testRate), testRate, 1)

	info, err := ProbeOpenFile(t.Context(), f, path)
	require.NoError(t, err)
	assert.Equal(t, 2, info.Channels)
}

func TestProbeOpenFile_NameSelectsDecoderByExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stored.bin")
	writeWAVChannels(t, path, make([]int16, testRate), testRate, 1)
	f := openForProbe(t, path)

	_, err := ProbeOpenFile(t.Context(), f, path)
	require.ErrorIs(t, err, ErrUnsupportedSource)

	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	info, err := ProbeOpenFile(t.Context(), f, "clip.wav")
	require.NoError(t, err)
	assert.Equal(t, testRate, info.SampleRate)
}

func TestProbeOpenFile_RejectsNonRegularHandle(t *testing.T) {
	f := openForProbe(t, t.TempDir())

	_, err := ProbeOpenFile(t.Context(), f, "clip.wav")

	require.ErrorIs(t, err, ErrUnsupportedSource)
	requireStatError(t, err, errors.CategoryValidation)
}

func TestProbeOpenFile_RejectsOversizedHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.wav")
	f, err := os.Create(path) //nolint:gosec // G304: test fixture path
	require.NoError(t, err)
	require.NoError(t, f.Truncate(maxSourceFileBytes+1))
	require.NoError(t, f.Close())

	_, err = ProbeOpenFile(t.Context(), openForProbe(t, path), path)

	require.ErrorIs(t, err, ErrAudioTooLong)
	requireStatError(t, err, errors.CategoryLimit)
}

func TestProbeOpenFile_ClosedHandleAndCanceled(t *testing.T) {
	path, _ := writeFixture(t, fixtureNamed(t, "wav"), probeTestRate, probeTestSeconds)
	f := openForProbe(t, path)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ProbeOpenFile(ctx, f, path)
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, f.Close())
	_, err = ProbeOpenFile(t.Context(), f, path)
	require.ErrorIs(t, err, os.ErrClosed)
	require.NotErrorIs(t, err, ErrUnsupportedSource)
	requireStatError(t, err, errors.CategoryFileIO)
}
