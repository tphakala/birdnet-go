package audionorm

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// These tests use FFmpeg's ebur128 filter as an independent reference oracle:
// the same PCM is measured by our pure-Go meter and by FFmpeg, and the results
// must agree. They are skipped automatically when ffmpeg is not installed.

func ffmpegPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found in PATH; skipping reference cross-validation")
	}
	return p
}

// float32ToFloat64 widens interleaved float32 PCM to float64 (test helper).
func float32ToFloat64(dst []float64, src []float32) {
	for i, s := range src {
		dst[i] = float64(s)
	}
}

// f32leBytes encodes interleaved float64 samples as little-endian float32 PCM.
func f32leBytes(samples []float64) []byte {
	buf := make([]byte, len(samples)*4)
	for i := range len(samples) {
		s := samples[i]
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(float32(s)))
	}
	return buf
}

var (
	reEbuI    = regexp.MustCompile(`(?m)^\s*I:\s*(-?[\d.]+|-?inf)\s*LUFS`)
	reEbuPeak = regexp.MustCompile(`(?m)^\s*Peak:\s*(-?[\d.]+|-?inf)\s*dBFS`)
)

// ffmpegEBUR128 runs ffmpeg's ebur128 scanner over the given interleaved PCM and
// returns the reported integrated loudness (LUFS) and true peak (dBTP).
func ffmpegEBUR128(t *testing.T, samples []float64, sampleRate, channels int) (integrated, truePeak float64) {
	t.Helper()
	bin := ffmpegPath(t)

	tmp, err := os.CreateTemp(t.TempDir(), "audionorm-*.f32le")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write(f32leBytes(samples)); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"-hide_banner", "-nostats",
		"-f", "f32le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels),
		"-i", tmp.Name(),
		"-af", "ebur128=peak=true", "-f", "null", "-",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg failed: %v\n%s", err, stderr.String())
	}

	out := stderr.String()
	mI := reEbuI.FindStringSubmatch(out)
	mP := reEbuPeak.FindStringSubmatch(out)
	if mI == nil || mP == nil {
		t.Fatalf("could not parse ffmpeg ebur128 summary:\n%s", out)
	}
	return parseFloatOrInf(t, mI[1]), parseFloatOrInf(t, mP[1])
}

func parseFloatOrInf(t *testing.T, s string) float64 {
	t.Helper()
	switch s {
	case "-inf":
		return math.Inf(-1)
	case "inf":
		return math.Inf(1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// compareToFFmpeg measures `samples` both ways and asserts agreement.
func compareToFFmpeg(t *testing.T, name string, samples []float64, sampleRate, channels int) {
	t.Helper()
	refI, refTP := ffmpegEBUR128(t, samples, sampleRate, channels)

	m := NewMeter(sampleRate, channels)
	m.AddFloat64(samples)
	gotI := m.IntegratedLoudness()
	gotTP := m.TruePeakDBTP()

	if math.Abs(gotI-refI) > 0.3 {
		t.Errorf("%s: integrated loudness %.3f LUFS vs ffmpeg %.3f (diff %.3f > 0.3)", name, gotI, refI, math.Abs(gotI-refI))
	}
	if math.Abs(gotTP-refTP) > 0.5 {
		t.Errorf("%s: true peak %.3f dBTP vs ffmpeg %.3f (diff %.3f > 0.5)", name, gotTP, refTP, math.Abs(gotTP-refTP))
	}
}

func TestFFmpegSineStereoLevels(t *testing.T) {
	for _, dbfs := range []float64{-6, -14, -23, -33} {
		compareToFFmpeg(t, "stereo sine "+strconv.Itoa(int(dbfs)),
			sineInterleaved(dbfs, 997, 4, 48000, 2), 48000, 2)
	}
}

func TestFFmpegSineMono(t *testing.T) {
	compareToFFmpeg(t, "mono sine -16",
		sineInterleaved(-16, 997, 4, 48000, 1), 48000, 1)
}

// Relative-gate behavior: two segments at different levels (EBU TC4/TC5 shape).
func TestFFmpegTwoSegmentGating(t *testing.T) {
	fs := 48000
	seg1 := sineInterleaved(-20, 997, 5, float64(fs), 2)
	seg2 := sineInterleaved(-30, 997, 5, float64(fs), 2)
	compareToFFmpeg(t, "gating -20/-30", append(seg1, seg2...), fs, 2)

	seg3 := sineInterleaved(-15, 997, 5, float64(fs), 2)
	compareToFFmpeg(t, "gating -20/-15", append(sineInterleaved(-20, 997, 5, float64(fs), 2), seg3...), fs, 2)
}

// White noise exercises the K-weighting filter across the spectrum, not just at
// one tone. Deterministic seed for reproducibility.
func TestFFmpegWhiteNoise(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	n := 48000 * 4 * 2
	samples := make([]float64, n)
	for i := range samples {
		samples[i] = (rng.Float64()*2 - 1) * 0.2 // ~-14 dBFS peak noise
	}
	compareToFFmpeg(t, "white noise", samples, 48000, 2)
}

// End-to-end: our library normalizes, then FFmpeg independently confirms the
// output loudness is on target and the true peak respects the ceiling. This is
// the full two-pass result validated against the reference.
func TestFFmpegNormalizeRoundTrip(t *testing.T) {
	ffmpegPath(t) // skip early if ffmpeg is missing

	// A quiet tone-plus-noise mix that needs a boost to reach target.
	rng := rand.New(rand.NewSource(7))
	n := 48000 * 4
	pcm := make([]float32, n)
	w := 2 * math.Pi * 1000 / 48000
	for i := range n {
		tone := 0.05 * math.Sin(w*float64(i))
		noise := (rng.Float64()*2 - 1) * 0.02
		pcm[i] = float32(tone + noise)
	}

	opts := DefaultOptions() // 48 kHz mono, -23 LUFS, -1.0 dBTP
	res, err := NormalizeFloat32(pcm, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.PeakLimited {
		t.Fatalf("unexpected peak limiting (gain %.2f dB)", res.GainDB)
	}

	out := make([]float64, len(pcm))
	float32ToFloat64(out, pcm)
	refI, refTP := ffmpegEBUR128(t, out, 48000, 1)

	if math.Abs(refI-opts.TargetLUFS) > 0.5 {
		t.Errorf("ffmpeg reports normalized loudness %.2f LUFS, want %.1f +/-0.5", refI, opts.TargetLUFS)
	}
	if refTP > opts.TruePeakDBTP+0.5 {
		t.Errorf("ffmpeg reports true peak %.2f dBTP, exceeds ceiling %.1f", refTP, opts.TruePeakDBTP)
	}
}

// A signal at 44.1 kHz checks that bilinear-transform coefficients are correct
// at a non-48k rate (FFmpeg recomputes the K-weighting too).
func TestFFmpegNon48kRate(t *testing.T) {
	compareToFFmpeg(t, "44.1k sine -18",
		sineInterleaved(-18, 1000, 4, 44100, 2), 44100, 2)
}
