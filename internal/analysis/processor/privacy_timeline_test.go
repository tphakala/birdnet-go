package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestItemAudioEnd_Fallbacks(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.PreCapture = 3

	tests := []struct {
		name string
		item classifier.Results
		want time.Time
	}{
		{
			name: "audio end wins",
			item: classifier.Results{AudioEndAt: base, AudioCapturedAt: base.Add(time.Hour), StartTime: base.Add(2 * time.Hour)},
			want: base,
		},
		{
			name: "captured time when no audio end",
			item: classifier.Results{AudioCapturedAt: base, StartTime: base.Add(2 * time.Hour)},
			want: base,
		},
		{
			name: "start time plus pre-capture plus chunk duration",
			item: classifier.Results{ModelID: testVADModel, StartTime: base, PCMdata: make([]byte, testVADChunkBytes)},
			want: base.Add(3*time.Second + 3*time.Second),
		},
		{
			name: "unknown model has no chunk duration",
			item: classifier.Results{ModelID: "no-such-model", StartTime: base, PCMdata: make([]byte, testVADChunkBytes)},
			want: base.Add(3 * time.Second),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, tt.want.Equal(itemAudioEnd(settings, &tt.item)))
		})
	}
}
