//go:build normcompare

package normbench

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

// lossyFormat is one FFmpeg-encoded export format under comparison, with the
// encoder and container the production export path selects for it (getEncoder
// and getOutputFormat in internal/audiocore/ffmpeg/clip.go, both unexported).
type lossyFormat struct {
	format    string
	encoder   string
	container string
	bitrate   string
}

// The formats FFmpeg still encodes on the export path. AAC and Opus reach it
// whenever their native-encoder gate is unset, which is every default install;
// MP3 has no native encoder at all.
var lossyFormats = []lossyFormat{
	{format: ffmpeg.FormatMP3, encoder: "libmp3lame", container: "mp3", bitrate: "192k"},
	{format: ffmpeg.FormatAAC, encoder: "aac", container: "mp4", bitrate: "192k"},
	{format: ffmpeg.FormatOpus, encoder: "libopus", container: "opus", bitrate: "96k"},
}

// TestCompareLossyFormats answers the question removing loudnorm actually raises:
// does an MP3, AAC or Opus clip come out at the same loudness as it did before?
//
// TestCompareNormalization compares gain planning through a lossless FLAC output,
// which isolates the normalisation decision but says nothing about the formats
// that still go through FFmpeg. This runs both paths end to end per format:
//
//   - before: FFmpeg's two-pass linear loudnorm filter, which is what the export
//     path built until it was removed
//   - after: the audionorm gain resolved in Go and handed to FFmpeg as a plain
//     volume filter, which is what buildFFmpegExportOptions now does
//
// Like the sibling harness this is a report, not a gate: it asserts only that
// both paths produce a measurable file. Read the table.
func TestCompareLossyFormats(t *testing.T) {
	ffmpegBin := ffmpegPath(t)
	cases := loadCorpus(t, ffmpegBin)
	dir := t.TempDir()
	ctx := t.Context()

	for _, lf := range lossyFormats {
		t.Run(lf.format, func(t *testing.T) {
			var deltas []float64
			var worst float64
			var worstCase string

			t.Logf("%-38s | %9s | %9s | %8s", "case", "before I", "after I", "delta")
			for i, tc := range cases {
				stem := filepath.Join(dir, fmt.Sprintf("%s-case%d", lf.format, i))
				wav := writeWAV(t, stem+"-in.wav", tc.pcm)

				before := exportLoudnorm(t, ctx, ffmpegBin, lf, wav, stem+"-before")
				after := exportWithResolvedGain(t, ctx, ffmpegBin, lf, tc.pcm, stem+"-after")

				delta := after.integrated - before.integrated
				if !math.IsInf(before.integrated, 0) && !math.IsInf(after.integrated, 0) {
					deltas = append(deltas, delta)
					if math.Abs(delta) > math.Abs(worst) {
						worst, worstCase = delta, tc.name
					}
				}
				t.Logf("%-38s | %9.2f | %9.2f | %+8.2f", tc.name, before.integrated, after.integrated, delta)
			}

			require.NotEmpty(t, deltas, "no comparable cases for %s", lf.format)
			var sum, sumAbs float64
			within := 0
			for _, d := range deltas {
				sum += d
				sumAbs += math.Abs(d)
				if math.Abs(d) <= 0.5 {
					within++
				}
			}
			n := float64(len(deltas))
			t.Logf("\n%s: %d comparable cases, mean %+.2f LU, mean abs %.2f LU, within 0.5 LU on %d/%d; worst %+.2f LU on %s",
				lf.format, len(deltas), sum/n, sumAbs/n, within, len(deltas), worst, worstCase)
		})
	}
}

// exportLoudnorm renders the clip the way the export path did before this change:
// FFmpeg's two-pass linear loudnorm, with the output rate pinned back because
// loudnorm upsamples internally for true-peak detection.
func exportLoudnorm(t *testing.T, ctx context.Context, ffmpegBin string, lf lossyFormat, wavPath, stem string) loudness {
	t.Helper()

	out := stem + "." + lf.container
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", wavPath,
		"-af", loudnormFilter(ctx, t, ffmpegBin, wavPath),
		"-ar", itoa(sampleRate),
		"-c:a", lf.encoder,
		"-b:a", lf.bitrate,
		"-f", lf.container,
		"-y", out,
	}
	runFFmpegCmd(t, ctx, ffmpegBin, args)
	return measure(t, ctx, ffmpegBin, out)
}

// exportWithResolvedGain renders the clip the way the export path does now: the
// gain is planned in Go and ExportAudio turns it into a volume filter. This calls
// the real production ExportAudio, so the argument list under test is the one
// that ships.
func exportWithResolvedGain(t *testing.T, ctx context.Context, ffmpegBin string, lf lossyFormat, pcm []byte, stem string) loudness {
	t.Helper()

	out := stem + "." + lf.container
	gainDB := planNative(measureNative(t, pcm), pcm).gainDB

	require.NoError(t, ffmpeg.ExportAudio(ctx, &ffmpeg.ExportOptions{
		PCMData:    pcm,
		OutputPath: out,
		Format:     lf.format,
		Bitrate:    lf.bitrate,
		SampleRate: sampleRate,
		Channels:   channels,
		BitDepth:   bitDepth,
		GainDB:     gainDB,
		FFmpegPath: ffmpegBin,
	}), "native-gain export")

	return measure(t, ctx, ffmpegBin, out)
}

func runFFmpegCmd(t *testing.T, ctx context.Context, ffmpegBin string, args []string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, ffmpegBin, args...) //nolint:gosec // G204: test-controlled paths
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "ffmpeg: %s", stderr.String())
}
