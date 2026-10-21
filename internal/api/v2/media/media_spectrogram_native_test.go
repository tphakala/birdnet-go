package media

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/mp3"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
	"github.com/tphakala/birdnet-go/internal/audiocore/wav"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

const (
	// nativeGateClipRate is the sample rate of the clips the no-binary test renders.
	nativeGateClipRate = 48000
	// nativeGateClipSeconds is their length.
	nativeGateClipSeconds = 3.0
	// nativeGateMarker replaces a rendered PNG to prove it is served, not redrawn.
	nativeGateMarker = "marker-not-a-png"
	// pngSignature starts every PNG file.
	pngSignature = "\x89PNG\r\n\x1a\n"
)

// restoreFFprobePath clears the configured ffprobe path for the test and puts
// back the value found on entry when the test ends.
func restoreFFprobePath(t *testing.T) {
	t.Helper()
	prior := ffmpeg.GetFFprobePath()
	ffmpeg.SetFFprobePath("")
	t.Cleanup(func() { ffmpeg.SetFFprobePath(prior) })
}

// clearBinaryEnvironment makes ffmpeg and ffprobe unresolvable by name or
// by configured path.
func clearBinaryEnvironment(t *testing.T, settings *conf.Settings) {
	t.Helper()
	t.Setenv("PATH", "")
	restoreFFprobePath(t)
	settings.Realtime.Audio.FfmpegPath = ""
}

// nativeClipWriters writes one clip per format through the production encoders.
var nativeClipWriters = map[string]func(t *testing.T, path string, pcm []byte){
	"wav": func(t *testing.T, path string, pcm []byte) {
		t.Helper()
		require.NoError(t, wav.EncodePCM(t.Context(), &wav.Options{PCMData: pcm, OutputPath: path, SampleRate: nativeGateClipRate, BitDepth: 16}))
	},
	"flac": func(t *testing.T, path string, pcm []byte) {
		t.Helper()
		require.NoError(t, flac.EncodePCM(t.Context(), &flac.Options{PCMData: pcm, OutputPath: path, SampleRate: nativeGateClipRate, Channels: 1, BitDepth: 16}))
	},
	"mp3": func(t *testing.T, path string, pcm []byte) {
		t.Helper()
		require.NoError(t, mp3.EncodePCM(t.Context(), &mp3.Options{PCMData: pcm, OutputPath: path, SampleRate: nativeGateClipRate, Channels: 1, BitDepth: 16}))
	},
	"m4a": func(t *testing.T, path string, pcm []byte) {
		t.Helper()
		require.NoError(t, aac.EncodePCM(t.Context(), &aac.Options{PCMData: pcm, OutputPath: path, SampleRate: nativeGateClipRate, Channels: 1, BitDepth: 16}))
	},
	"opus": func(t *testing.T, path string, pcm []byte) {
		t.Helper()
		require.NoError(t, opus.EncodePCM(t.Context(), &opus.Options{PCMData: pcm, OutputPath: path, SampleRate: nativeGateClipRate, Channels: 1, BitDepth: 16}))
	},
}

func TestGenerateSpectrogramFromRel_NativeNeedsNoBinaries(t *testing.T) {
	for format, write := range nativeClipWriters {
		t.Run(format, func(t *testing.T) {
			withRestoredGlobalSettings(t)

			tmp := t.TempDir()
			sfs, err := securefs.New(tmp)
			require.NoError(t, err)
			t.Cleanup(func() { _ = sfs.Close() })

			settings := apitest.NewValidTestSettings()
			clearBinaryEnvironment(t, settings)

			ctx := t.Context()
			core := &apicore.Core{SFS: sfs}
			core.SetTestContext(ctx, nil)
			h := &Handler{Core: core}
			h.Settings.Store(settings)
			h.spectrogramGenerator = spectrogram.NewGenerator(settings, sfs, nil)

			relAudio := "2024/06/04/Turdus_merula_80p." + format
			absAudio := filepath.Join(tmp, relAudio)
			require.NoError(t, os.MkdirAll(filepath.Dir(absAudio), 0o750))
			write(t, absAudio, toneBytes(nativeGateClipRate, nativeGateClipSeconds, fixtureToneHz, fixtureAmp))

			const width = SpectrogramSizeLg
			_, _, _, relSpec := buildSpectrogramPaths(relAudio, width, true, "", "", "")

			got, err := h.generateSpectrogramFromRel(ctx, relAudio, relAudio, "", width, true, "", "", "")
			require.NoError(t, err)
			assert.Equal(t, relSpec, got)

			absSpec := filepath.Join(tmp, relSpec)
			data, err := os.ReadFile(absSpec)
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(data, []byte(pngSignature)), "the output is a PNG")

			// A PNG at the final path is served, never redrawn.
			require.NoError(t, os.WriteFile(absSpec, []byte(nativeGateMarker), 0o600))
			_, err = h.generateSpectrogramFromRel(ctx, relAudio, relAudio, "", width, true, "", "", "")
			require.NoError(t, err)
			again, err := os.ReadFile(absSpec)
			require.NoError(t, err)
			assert.Equal(t, nativeGateMarker, string(again))
		})
	}
}
