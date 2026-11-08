package detection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestResult_IsSpectrogramOnly pins that only a missing audio clip with a kept
// spectrogram counts as spectrogram-only.
func TestResult_IsSpectrogramOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		clipName            string
		spectrogramClipName string
		want                bool
	}{
		{"no audio, spectrogram kept", "", "2026/06/a.wav", true},
		{"audio and spectrogram", "2026/06/a.wav", "2026/06/a.wav", false},
		{"audio only", "2026/06/a.wav", "", false},
		{"neither", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &Result{ClipName: tt.clipName, SpectrogramClipName: tt.spectrogramClipName}
			assert.Equal(t, tt.want, r.IsSpectrogramOnly())
		})
	}
}

// TestResultModelContrib_HasHitStats pins each condition of the extended-statistics
// unit: a missing hit count or either missing hit time must read as "no stats", so
// the datastore stores NULL instead of a year-0001 Unix millisecond value.
func TestResultModelContrib_HasHitStats(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		c    ResultModelContrib
		want bool
	}{
		{"all present", ResultModelContrib{HitCount: 1, FirstHitAt: at, LastHitAt: at}, true},
		{"no hits", ResultModelContrib{FirstHitAt: at, LastHitAt: at}, false},
		{"first time missing", ResultModelContrib{HitCount: 1, LastHitAt: at}, false},
		{"last time missing", ResultModelContrib{HitCount: 1, FirstHitAt: at}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.c.HasHitStats())
		})
	}
}
