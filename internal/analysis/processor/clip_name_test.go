package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
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
			clipName := p.generateClipNameWithDuration(tt.scientificName, tt.confidence, tt.durationSecs, time.Now())
			for _, want := range tt.wantContains {
				assert.Contains(t, clipName, want)
			}
		})
	}
}
