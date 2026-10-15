package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// itemAudioEnd returns the capture time of the last sample of the chunk in item:
// item.AudioEndAt when the producer set it, else item.AudioCapturedAt, else
// StartTime advanced by PreCapture and the chunk's PCM duration (StartTime is
// back-dated by PreCapture plus the clip length at read time, so this
// reconstructs the read time; only tests and hypothetical producers that set
// none of the audio times reach it).
func itemAudioEnd(settings *conf.Settings, item *classifier.Results) time.Time {
	if !item.AudioEndAt.IsZero() {
		return item.AudioEndAt
	}
	if !item.AudioCapturedAt.IsZero() {
		return item.AudioCapturedAt
	}
	end := item.StartTime.Add(time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second)
	if spec, ok := classifier.GetModelSpec(item.ModelID); ok {
		end = end.Add(pcmChunkDuration(len(item.PCMdata), spec.EffectiveSampleRate()))
	}
	return end
}
