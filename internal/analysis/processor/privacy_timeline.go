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

// recordHumanDetection records a speech hit on source at the capture time of the
// end of the audio that produced it. The hit time never moves backward: the
// discard check reads only the latest value, so a later write with an earlier
// stamp (the label path after the VAD within one chunk, or a 5 s model's chunk
// after a 3 s model's) would hide an earlier-reaching hit. An equal time keeps
// the existing entry and its trigger.
func (p *Processor) recordHumanDetection(source string, at time.Time, trigger string) {
	p.detectionMutex.Lock()
	defer p.detectionMutex.Unlock()
	if existing, ok := p.LastHumanDetection[source]; ok && !at.After(existing.Time) {
		return
	}
	p.LastHumanDetection[source] = HumanDetection{Time: at, Trigger: trigger}
}
