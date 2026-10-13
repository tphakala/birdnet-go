package processor

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
)

// noLockedClipsDB is a diskmanager.Interface with no locked clips.
type noLockedClipsDB struct{}

func (noLockedClipsDB) GetLockedNotesClipPaths() ([]string, error)        { return nil, nil }
func (noLockedClipsDB) ClearNoteClipPathsByNames([]string) (int64, error) { return 0, nil }
func (noLockedClipsDB) RetainNoteSpectrogramsByClipNames([]string) (int64, error) {
	return 0, nil
}

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

	got := p.buildClipPath(p.Settings, "Strix aluco", 0.94, 15, ts)

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
			got := p.buildClipPath(p.Settings, "Strix aluco", 0.94, 15, ts)
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
			_ = p.buildClipPath(p.Settings, "Strix aluco", 0.94, 15, ts)
		})
	}
	wg.Wait()

	// sync.Once guarantees at-most-one; the bool confirms it fired at least
	// once under concurrent load. Exact-once is a stdlib invariant.
	assert.True(t, buildClipPathFallbackWarned(),
		"expected sync.Once to have fired after empty-Type calls")
}

// TestBuildClipPath_PerchSoundClassRoundTrip proves that a clip path built for a
// full Perch sound-class label (underscores and parentheses) is read back by the
// diskmanager scanner with the same species stem, confidence and extension.
func TestBuildClipPath_PerchSoundClassRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		label string
		want  string
	}{
		{"parentheses", "Bathtub_(filling_or_washing)", "bathtub_(filling_or_washing)"},
		{"many underscores", "Domestic_sounds_and_home_sounds", "domestic_sounds_and_home_sounds"},
		{"single underscore", "Power_tool", "power_tool"},
	}
	ts := time.Date(2026, 3, 14, 9, 37, 1, 0, time.UTC)
	for _, tt := range tests {
		for _, duration := range []int{0, 15} {
			t.Run(fmt.Sprintf("%s/duration=%d", tt.name, duration), func(t *testing.T) {
				p := newProcessorWithExportType(t, "wav")
				rel := p.buildClipPath(p.Settings, tt.label, 0.94, duration, ts)

				dir := t.TempDir()
				full := filepath.Join(dir, filepath.FromSlash(rel))
				require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
				require.NoError(t, os.WriteFile(full, []byte("x"), 0o600))

				files, err := diskmanager.GetAudioFiles(dir, []string{".wav"}, noLockedClipsDB{})
				require.NoError(t, err)
				require.Len(t, files, 1)
				assert.Equal(t, tt.want, files[0].Species)
				assert.Equal(t, 94, files[0].Confidence)
			})
		}
	}
}
