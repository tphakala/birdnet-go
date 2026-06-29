package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// AudibleBatsOutputSampleRate is the sample rate (Hz) of the derived "audible
// bats" review audio. Ultrasonic clips are time-expanded and then resampled to
// this standard playback rate so any browser can play the result.
const AudibleBatsOutputSampleRate = 48000

// validBatExpansionFactors is the set of time-expansion factors offered for
// audible-bats playback. A larger factor slows the audio more, lowering
// ultrasonic calls further into the human hearing range.
var validBatExpansionFactors = map[int]bool{5: true, 10: true, 16: true, 20: true}

// IsValidBatExpansionFactor reports whether n is an offered time-expansion factor.
func IsValidBatExpansionFactor(n int) bool {
	return validBatExpansionFactors[n]
}

// timeExpandTimeout bounds the time-expansion FFmpeg pass.
const timeExpandTimeout = 60 * time.Second

// sampleRateProbeTimeout bounds the ffprobe sample-rate query on a local file.
const sampleRateProbeTimeout = 5 * time.Second

// ProbeFileSampleRate returns the sample rate (Hz) of the first audio stream in
// filePath using ffprobe. Unlike ProbeStreamInfo — which whitelists network
// protocols for live streams and therefore rejects the local `file` protocol —
// this probes a local file path directly.
func ProbeFileSampleRate(ctx context.Context, filePath string) (int, error) {
	ffprobeBinary, err := resolveFFprobeBinary()
	if err != nil {
		return 0, err
	}

	probeCtx, cancel := context.WithTimeout(ctx, sampleRateProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, ffprobeBinary, //nolint:gosec // G204: ffprobeBinary validated by config or exec.LookPath, filePath validated by caller
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=sample_rate",
		"-of", "json",
		filePath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if probeCtx.Err() != nil {
			return 0, fmt.Errorf("sample-rate probe timed out: %w", probeCtx.Err())
		}
		return 0, fmt.Errorf("sample-rate probe failed: %w (stderr: %s)", err, stderr.String())
	}

	info, err := parseProbeOutput(stdout.Bytes())
	if err != nil {
		return 0, err
	}
	return info.SampleRate, nil
}

// TimeExpandBatAudio applies time expansion to an ultrasonic clip and writes a
// mono-preserving WAV resampled to AudibleBatsOutputSampleRate at outputPath.
//
// The source is slowed and pitched down by expansionFactor via FFmpeg's asetrate
// filter, which relabels the stream's sample rate: a 40 kHz bat call expanded 10x
// plays at 4 kHz, well inside the human hearing range. The result is then
// resampled to the standard output rate.
//
// No normalization or gain is applied here. The caller applies those after
// conversion, because audible background noise in the original clip would
// otherwise skew loudness normalization computed before the ultrasonic content
// is shifted into the audible band.
func TimeExpandBatAudio(ctx context.Context, inputPath, ffmpegPath string, expansionFactor, sourceSampleRate int, outputPath string) error {
	if err := ValidateFFmpegPath(ffmpegPath); err != nil {
		return fmt.Errorf("invalid FFmpeg path: %w", err)
	}
	if !IsValidBatExpansionFactor(expansionFactor) {
		return fmt.Errorf("invalid time-expansion factor: %d", expansionFactor)
	}
	if sourceSampleRate <= 0 {
		return fmt.Errorf("invalid source sample rate: %d", sourceSampleRate)
	}

	ctx, cancel := context.WithTimeout(ctx, timeExpandTimeout)
	defer cancel()

	// asetrate relabels the stream's sample rate, slowing playback and lowering
	// pitch by expansionFactor; aresample then converts to the standard output rate.
	expandedRate := sourceSampleRate / expansionFactor
	if expandedRate <= 0 {
		return fmt.Errorf("expanded sample rate is non-positive (source=%d, factor=%d)", sourceSampleRate, expansionFactor)
	}
	filterChain := fmt.Sprintf("asetrate=%d,aresample=%d", expandedRate, AudibleBatsOutputSampleRate)

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y", // overwrite output file
		"-i", inputPath,
		"-af", filterChain,
		"-c:a", "pcm_s16le",
		outputPath,
	}

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath validated by ValidateFFmpegPath, args built internally
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("time expansion cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("time expansion failed: %w, stderr: %s", err, stderr.String())
	}

	return nil
}
