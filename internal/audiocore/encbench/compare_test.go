//go:build enccompare && unix

package encbench

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
)

const (
	sampleRate = 48000
	channels   = 1
	bitDepth   = 16

	// clipSeconds matches BirdNET-Go's default detection clip length, so the
	// numbers translate directly to per-detection cost.
	clipSeconds = 15.0

	// iterations is enough to average out scheduler noise without making a run
	// on a Raspberry Pi tedious.
	iterations = 20
)

// usage is the per-encode cost of one contender. The two memory figures are
// deliberately different measurements, because the two paths spend memory in
// different places: a native encoder allocates on this process's Go heap, while
// FFmpeg pays for a whole separate process image. heapPerOp captures the
// former and childRSSKB the latter. childRSSKB reads zero only until the first
// FFmpeg row; after that every row inherits that peak, because the underlying
// counter is a high-water mark (see the closing note the report prints).
type usage struct {
	wall       time.Duration
	cpu        time.Duration
	heapPerOp  uint64
	childRSSKB int64
}

func snapshot(t *testing.T) (self, children syscall.Rusage) {
	t.Helper()
	require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &self))
	require.NoError(t, syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children))
	return self, children
}

func cpuOf(r *syscall.Rusage) time.Duration {
	return time.Duration(r.Utime.Nano()) + time.Duration(r.Stime.Nano())
}

// measure runs encode iterations times and reports the average per-encode cost.
// CPU time sums this process and its children, so the FFmpeg fork/exec and its
// decode of the raw PCM on stdin are both counted.
//
// RUSAGE_CHILDREN.Maxrss is the peak RSS of any reaped child, so it reads as
// zero until FFmpeg has run at least once and thereafter reports the FFmpeg
// process image. RUSAGE_SELF.Maxrss is deliberately not reported: it is a
// monotonic high-water mark for the whole test binary and would show the same
// number for every contender.
func measure(t *testing.T, encode func() error) usage {
	t.Helper()
	// One warm-up so pooled encoders and the page cache are primed for both
	// contenders alike.
	require.NoError(t, encode())

	var heapBefore, heapAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&heapBefore)
	selfBefore, childBefore := snapshot(t)

	start := time.Now()
	for range iterations {
		require.NoError(t, encode())
	}
	wall := time.Since(start)

	selfAfter, childAfter := snapshot(t)
	runtime.ReadMemStats(&heapAfter)

	return usage{
		wall: wall / iterations,
		cpu: (cpuOf(&selfAfter) - cpuOf(&selfBefore) +
			cpuOf(&childAfter) - cpuOf(&childBefore)) / iterations,
		heapPerOp:  (heapAfter.TotalAlloc - heapBefore.TotalAlloc) / iterations,
		childRSSKB: childAfter.Maxrss,
	}
}

// pcmClip builds a clip with some spectral variety, so the coders do real work
// rather than trivially coding a single tone.
func pcmClip() []byte {
	n := int(sampleRate * clipSeconds)
	b := make([]byte, n*2)
	for i := range n {
		tt := float64(i) / sampleRate
		v := 0.35*math.Sin(2*math.Pi*1200*tt) +
			0.25*math.Sin(2*math.Pi*3400*tt) +
			0.15*math.Sin(2*math.Pi*7100*tt)
		s := int16(v * 24000)
		b[i*2] = byte(s)
		b[i*2+1] = byte(s >> 8)
	}
	return b
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	require.NoError(t, err)
	return st.Size()
}

// TestCompareEncoders prints a side-by-side table of native versus FFmpeg cost
// for each format that has both paths. It asserts nothing about the numbers;
// the point is the report.
func TestCompareEncoders(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; nothing to compare against")
	}

	pcm := pcmClip()
	dir := t.TempDir()
	ctx := t.Context()

	ffmpegEncode := func(format, out string, bitrate string) func() error {
		return func() error {
			return ffmpeg.ExportAudio(ctx, &ffmpeg.ExportOptions{
				PCMData:    pcm,
				OutputPath: out,
				Format:     format,
				Bitrate:    bitrate,
				SampleRate: sampleRate,
				Channels:   channels,
				BitDepth:   bitDepth,
				FFmpegPath: ffmpegPath,
			})
		}
	}

	type contender struct {
		label  string
		out    string
		encode func() error
	}
	type row struct {
		format string
		native contender
		ff     contender
	}

	aacOutNative := filepath.Join(dir, "native.m4a")
	aacOutFF := filepath.Join(dir, "ffmpeg.m4a")
	opusOutNative := filepath.Join(dir, "native.opus")
	opusOutFF := filepath.Join(dir, "ffmpeg.opus")
	flacOutNative := filepath.Join(dir, "native.flac")
	flacOutFF := filepath.Join(dir, "ffmpeg.flac")

	rows := []row{
		{
			format: "aac (.m4a) @96k",
			native: contender{label: "native", out: aacOutNative, encode: func() error {
				return aac.EncodePCM(ctx, &aac.Options{
					PCMData: pcm, OutputPath: aacOutNative, SampleRate: sampleRate,
					Channels: channels, BitDepth: bitDepth, BitrateKbps: 96,
				})
			}},
			ff: contender{label: "ffmpeg", out: aacOutFF, encode: ffmpegEncode(ffmpeg.FormatAAC, aacOutFF, "96k")},
		},
		{
			format: "opus (.opus) @64k",
			native: contender{label: "native", out: opusOutNative, encode: func() error {
				return opus.EncodePCM(ctx, &opus.Options{
					PCMData: pcm, OutputPath: opusOutNative, SampleRate: sampleRate,
					Channels: channels, BitDepth: bitDepth, BitrateKbps: 64,
				})
			}},
			ff: contender{label: "ffmpeg", out: opusOutFF, encode: ffmpegEncode(ffmpeg.FormatOpus, opusOutFF, "64k")},
		},
		{
			// FLAC is already native in production; included as a control, since
			// its native-versus-FFmpeg ratio is a known-good reference point.
			format: "flac (control)",
			native: contender{label: "native", out: flacOutNative, encode: func() error {
				return flac.EncodePCM(ctx, &flac.Options{
					PCMData: pcm, OutputPath: flacOutNative, SampleRate: sampleRate,
					Channels: channels, BitDepth: bitDepth,
				})
			}},
			ff: contender{label: "ffmpeg", out: flacOutFF, encode: ffmpegEncode(ffmpeg.FormatFLAC, flacOutFF, "")},
		},
	}

	out := t.Output()
	// t.Output() is a plain io.Writer; its writes cannot meaningfully fail here,
	// and threading an error through a report printer would only add noise.
	printf := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
	printf("\n%.0fs mono %d Hz clip, %d iterations each\n\n", clipSeconds, sampleRate, iterations)
	printf("%-20s %-8s %10s %10s %10s %12s %12s %12s\n",
		"format", "encoder", "wall", "cpu", "xrealtime", "size", "heap/op", "child rss")
	printf("%s\n", "-------------------------------------------------------------------------------------------------------")

	for _, r := range rows {
		for _, c := range []contender{r.native, r.ff} {
			u := measure(t, c.encode)
			realtime := clipSeconds * float64(time.Second) / float64(u.wall)
			printf("%-20s %-8s %10s %10s %9.0fx %10d B %9d KB %9d KB\n",
				r.format, c.label,
				u.wall.Round(time.Millisecond/10), u.cpu.Round(time.Millisecond/10),
				realtime, fileSize(t, c.out), u.heapPerOp/1024, u.childRSSKB)
		}
		printf("\n")
	}

	printf("heap/op is Go heap allocated per encode inside this process.\n")
	printf("child rss is RUSAGE_CHILDREN.Maxrss, a monotonic high-water mark over every\n")
	printf("child reaped so far: it is the FFmpeg process image, and once FFmpeg has run\n")
	printf("once every later row inherits that peak. Read it as the cost FFmpeg imposes,\n")
	printf("not as a per-row figure; a native encoder spawns no child at all.\n")
}
