//go:build normcompare

package normbench

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	// The export PCM shape. Every clip BirdNET-Go normalizes is 48 kHz, mono,
	// 16-bit (conf.SampleRate / conf.NumChannels / conf.BitDepth), so the corpus
	// is decoded to exactly that rather than to whatever the source file holds.
	sampleRate = 48000
	channels   = 1
	bitDepth   = 16

	bytesPerSample = bitDepth / 8
	bytesPerSecond = sampleRate * channels * bytesPerSample

	// clipSeconds matches the default detection clip length, so the measured
	// divergence is the divergence a real export sees. It also sits far above
	// the 400 ms EBU R128 gating block, which a shorter clip would fall under.
	clipSeconds = 15

	// clipsPerSource caps how much of a long recording is used, so a 2-minute
	// soundscape does not dominate the report.
	clipsPerSource = 3
)

// clipBytes is the size of one detection-length clip at the export PCM shape.
// Sizing by the export rate rather than a fixed sample count is deliberate:
// a fixed count silently falls under the 400 ms gate at high rates.
const clipBytes = clipSeconds * bytesPerSecond

// testCase is one clip to run through both normalization paths.
type testCase struct {
	// name identifies the source recording and the loudness case applied to it.
	name string
	// why states what the case is meant to exercise, so a surprising row in the
	// report can be read without cross-referencing the generator.
	why string
	pcm []byte
}

// ffmpegPath resolves the ffmpeg binary the harness shells out to for decoding
// the corpus and for measuring outputs.
func ffmpegPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("BIRDNET_NORMCOMPARE_FFMPEG"); p != "" {
		return p
	}
	p, err := exec.LookPath("ffmpeg")
	require.NoError(t, err, "normcompare needs ffmpeg on PATH or BIRDNET_NORMCOMPARE_FFMPEG set")
	return p
}

// corpusDir returns the directory holding the source recordings. It defaults to
// the repository root, which carries the checked-in field recordings.
func corpusDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("BIRDNET_NORMCOMPARE_CORPUS"); d != "" {
		return d
	}
	// This package sits at internal/audiocore/normbench.
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	return root
}

// loadCorpus decodes every WAV in the corpus directory to the export PCM shape
// and slices each into detection-length clips.
func loadCorpus(t *testing.T, ffmpeg string) []testCase {
	t.Helper()

	dir := corpusDir(t)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "reading corpus directory %s", dir)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".wav") {
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names) // stable report ordering
	require.NotEmpty(t, names, "no .wav recordings found in %s; set BIRDNET_NORMCOMPARE_CORPUS", dir)

	var cases []testCase
	for _, name := range names {
		pcm := decodeToExportPCM(t, ffmpeg, filepath.Join(dir, name))
		base := strings.TrimSuffix(name, filepath.Ext(name))

		for i := range clipsPerSource {
			start := i * clipBytes
			if start+clipBytes > len(pcm) {
				break
			}
			clip := pcm[start : start+clipBytes]
			cases = append(cases, expandCases(base, i, clip)...)
		}
	}
	require.NotEmpty(t, cases, "corpus produced no clips of %d s", clipSeconds)
	return cases
}

// decodeToExportPCM decodes any WAV to headerless 48 kHz mono 16-bit PCM, the
// same bytes the export path receives from the capture buffer.
func decodeToExportPCM(t *testing.T, ffmpeg, path string) []byte {
	t.Helper()
	cmd := exec.Command(ffmpeg, //nolint:gosec // G204: harness-only, paths come from the test environment
		"-hide_banner", "-v", "error",
		"-i", path,
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ar", itoa(sampleRate),
		"-ac", itoa(channels),
		"-",
	)
	out, err := cmd.Output()
	require.NoError(t, err, "decoding %s", path)
	require.NotEmpty(t, out, "decoding %s produced no PCM", path)
	return out
}

// expandCases turns one clip into the loudness cases that separate the two
// implementations. A clip as captured usually sits inside FFmpeg's linear gate
// and above the R128 absolute gate, where the two agree; the derived cases push
// it outside those bounds on purpose.
func expandCases(base string, idx int, clip []byte) []testCase {
	id := func(kind string) string { return base + "#" + itoa(idx) + "/" + kind }

	return []testCase{
		{
			name: id("as-captured"),
			why:  "baseline: the clip as the capture buffer delivers it",
			pcm:  cloneScaled(clip, 1),
		},
		{
			name: id("quiet-20dB"),
			why:  "a distant or gain-starved recording that still measures above the R128 gate",
			pcm:  cloneScaled(clip, dbToFactor(-20)),
		},
		{
			name: id("quiet-40dB"),
			why:  "so quiet that R128 gating cannot measure it, which only the gate fallback rescues",
			pcm:  cloneScaled(clip, dbToFactor(-40)),
		},
		{
			name: id("hot"),
			why:  "recorded near full scale, so normalization must attenuate",
			pcm:  cloneScaled(clip, dbToFactor(+12)),
		},
		{
			name: id("transient-over-quiet-bed"),
			why:  "loud call over a quiet bed: high LRA, one of the conditions that voids linear=true",
			pcm:  transientOverQuietBed(clip),
		},
	}
}

// transientOverQuietBed attenuates the clip into a quiet bed and restores full
// level over one short window, producing the wide loudness range that pushes
// FFmpeg's loudnorm out of linear mode even when linear=true is requested.
func transientOverQuietBed(clip []byte) []byte {
	out := cloneScaled(clip, dbToFactor(-30))

	// One second of the original level, a third of the way in: long enough to
	// dominate the gated integrated loudness, short enough to leave the bed as
	// the clip's character.
	burstStart := (len(out) / 3) &^ 1 // keep the offset sample-aligned
	burstEnd := burstStart + bytesPerSecond
	if burstEnd > len(out) {
		burstEnd = len(out)
	}
	copy(out[burstStart:burstEnd], clip[burstStart:burstEnd])
	return out
}

// cloneScaled copies int16 PCM with a linear amplitude factor, saturating rather
// than wrapping so an amplified clip clips the way real hot audio does.
func cloneScaled(src []byte, factor float64) []byte {
	dst := make([]byte, len(src))
	if factor == 1 {
		copy(dst, src)
		return dst
	}
	for i := 0; i+1 < len(src); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(src[i:]))) * factor
		binary.LittleEndian.PutUint16(dst[i:], uint16(saturateInt16(v)))
	}
	return dst
}

func saturateInt16(v float64) int16 {
	r := math.Round(v)
	switch {
	case r > math.MaxInt16:
		return math.MaxInt16
	case r < math.MinInt16:
		return math.MinInt16
	default:
		return int16(r)
	}
}

func dbToFactor(db float64) float64 { return math.Pow(10, db/20) }

// itoa avoids pulling strconv into every call site for the small integers here.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
