package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

func TestGenerateClipNameWithDuration(t *testing.T) {
	t.Parallel()

	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{Type: "wav"},
				},
			},
		},
	}

	tests := []struct {
		name           string
		scientificName string
		confidence     float32
		durationSecs   int
		wantContains   []string
	}{
		{
			name:           "normal 15s clip",
			scientificName: "Parus major",
			confidence:     0.72,
			durationSecs:   15,
			wantContains:   []string{"parus_major", "72p", "15s", ".wav"},
		},
		{
			name:           "extended 312s clip",
			scientificName: "Strix uralensis",
			confidence:     0.85,
			durationSecs:   312,
			wantContains:   []string{"strix_uralensis", "85p", "312s", ".wav"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clipName := p.generateClipNameWithDuration(p.Settings, tt.scientificName, tt.confidence, tt.durationSecs, time.Now())
			for _, want := range tt.wantContains {
				assert.Contains(t, clipName, want)
			}
		})
	}
}

// newClipGatingProcessor builds a minimal Processor whose ClipName-generation
// path (resolveClipName) can run without a real BirdNET instance or registry.
func newClipGatingProcessor(exportEnabled bool) *Processor {
	return &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Enabled: exportEnabled,
						Type:    "wav",
						Length:  15,
					},
				},
			},
		},
	}
}

// TestResolveClipName_ExportGating verifies that a clip name is only generated
// when audio export is enabled. When export is off, ClipName must be empty so it
// remains a truthful per-detection signal (issue: truthful clip_name).
func TestResolveClipName_ExportGating(t *testing.T) {
	t.Parallel()

	item := &classifier.Results{
		Source:  datastore.AudioSource{ID: "test_source"},
		ModelID: "",
	}

	t.Run("export disabled yields empty clip name", func(t *testing.T) {
		t.Parallel()
		p := newClipGatingProcessor(false)
		clipName := p.resolveClipName(p.Settings, item, "Parus major", 0.85)
		assert.Empty(t, clipName,
			"clip name must be empty when export is disabled so it stays a truthful signal")
	})

	t.Run("export enabled yields non-empty clip name", func(t *testing.T) {
		t.Parallel()
		p := newClipGatingProcessor(true)
		clipName := p.resolveClipName(p.Settings, item, "Parus major", 0.85)
		require.NotEmpty(t, clipName, "clip name must be generated when export is enabled")
		assert.Contains(t, clipName, "parus_major")
		assert.Contains(t, clipName, ".wav")
	})
}

// TestNormalizeDetectionTimes_ExtendedCapture_ExportGating verifies the extended
// capture path only regenerates the clip name when export is enabled, leaving a
// truthful empty ClipName otherwise (even though EndTime is still recomputed).
func TestNormalizeDetectionTimes_ExtendedCapture_ExportGating(t *testing.T) {
	t.Parallel()

	newItem := func() *PendingDetection {
		firstDetected := time.Date(2026, 3, 7, 20, 0, 0, 0, time.UTC)
		return &PendingDetection{
			Detection: Detections{
				Result: detection.Result{
					BeginTime: firstDetected.Add(30 * time.Second),
					EndTime:   firstDetected.Add(84 * time.Second),
					Timestamp: firstDetected,
					Species: detection.Species{
						CommonName:     "lehtopöllö",
						ScientificName: "Strix aluco",
					},
					ClipName: "",
				},
			},
			Confidence:      0.92,
			Source:          "test_source",
			FirstDetected:   firstDetected,
			LastUpdated:     firstDetected.Add(3 * time.Minute),
			Count:           45,
			ExtendedCapture: true,
		}
	}

	t.Run("export disabled leaves clip name empty", func(t *testing.T) {
		t.Parallel()
		p := &Processor{
			Settings: &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						Export: conf.ExportSettings{Enabled: false, Length: 60, PreCapture: 6, Type: "wav"},
					},
					ExtendedCapture: conf.ExtendedCaptureSettings{Enabled: true, MaxDuration: 600},
				},
			},
		}
		item := newItem()
		p.normalizeDetectionTimes(item)
		assert.Empty(t, item.Detection.Result.ClipName,
			"extended capture must not regenerate a clip name when export is disabled")
	})

	t.Run("export enabled regenerates clip name with duration", func(t *testing.T) {
		t.Parallel()
		p := &Processor{
			Settings: &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						Export: conf.ExportSettings{Enabled: true, Length: 60, PreCapture: 6, Type: "wav"},
					},
					ExtendedCapture: conf.ExtendedCaptureSettings{Enabled: true, MaxDuration: 600},
				},
			},
		}
		item := newItem()
		p.normalizeDetectionTimes(item)
		require.NotEmpty(t, item.Detection.Result.ClipName,
			"extended capture must regenerate a clip name when export is enabled")
		assert.Contains(t, item.Detection.Result.ClipName, "strix_aluco")
	})
}
