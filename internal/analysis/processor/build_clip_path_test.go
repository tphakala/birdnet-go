package processor

import (
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// newProcessorWithExportType returns a minimal Processor suitable for
// exercising buildClipPath in isolation. Only the fields buildClipPath
// reads are populated.
func newProcessorWithExportType(t *testing.T, exportType string) *Processor {
	t.Helper()
	return &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Type: exportType,
					},
				},
			},
		},
	}
}

func TestBuildClipPath_EmptyTypeFallsBackToWav(t *testing.T) {
	// Resets the package-level sync.Once so this test is order-independent.
	resetBuildClipPathFallbackOnce()
	t.Cleanup(resetBuildClipPathFallbackOnce)

	p := newProcessorWithExportType(t, "")
	ts := time.Date(2026, 3, 14, 9, 37, 1, 0, time.UTC)

	got := p.buildClipPath("Strix aluco", 0.94, 15, ts)

	assert.True(t, strings.HasSuffix(got, ".wav"),
		"empty Type should fall back to .wav, got %q", got)
	assert.False(t, strings.HasSuffix(got, "."),
		"path must never end in a bare dot, got %q", got)
}

func TestBuildClipPath_NeverEndsInDot(t *testing.T) {
	// Class-of-bug invariant: regardless of Type value, the returned path
	// must not end in a bare dot.
	resetBuildClipPathFallbackOnce()
	t.Cleanup(resetBuildClipPathFallbackOnce)

	inputs := []string{"", " ", "wav", "mp3", "aac", "opus", "flac", "garbage"}
	ts := time.Date(2026, 3, 14, 9, 37, 1, 0, time.UTC)

	for _, in := range inputs {
		t.Run("type="+in, func(t *testing.T) {
			p := newProcessorWithExportType(t, in)
			got := p.buildClipPath("Strix aluco", 0.94, 15, ts)
			assert.False(t, strings.HasSuffix(got, "."),
				"path must never end in bare dot (type=%q, got %q)", in, got)
			ext := strings.TrimPrefix(strings.TrimSpace(path.Ext(got)), ".")
			assert.NotEmpty(t, ext,
				"path must have a non-empty, non-whitespace extension (type=%q, got %q)", in, got)
		})
	}
}

func TestBuildClipPath_FallbackWarnsOnlyOnce(t *testing.T) {
	resetBuildClipPathFallbackOnce()
	t.Cleanup(resetBuildClipPathFallbackOnce)

	// Drive the fallback path 10 times in quick succession.
	p := newProcessorWithExportType(t, "")
	ts := time.Date(2026, 3, 14, 9, 37, 1, 0, time.UTC)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_ = p.buildClipPath("Strix aluco", 0.94, 15, ts)
		})
	}
	wg.Wait()

	// sync.Once guarantees at-most-one; the bool confirms it fired at least
	// once under concurrent load. Exact-once is a stdlib invariant.
	assert.True(t, buildClipPathFallbackWarned(),
		"expected sync.Once to have fired after empty-Type calls")
}
