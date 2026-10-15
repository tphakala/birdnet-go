package transcode

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// timeExpand slows p by factor and returns it at AudibleBatsOutputSampleRate,
// which lowers ultrasonic content by the same factor into the audible range. It
// is the native equivalent of relabeling the samples at rate/factor and
// resampling to 48 kHz: the audio is resampled by the ratio 48000*factor/rate and
// the result is then labeled 48 kHz. A source already at 48000*factor is a pure
// relabel. The expanded size must not exceed maxBytes.
//
// Normalization must run on the result, not before, so it measures what is
// played.
func timeExpand(ctx context.Context, p *pcmBuffer, factor, maxBytes int) (*pcmBuffer, error) {
	if !IsValidBatExpansionFactor(factor) {
		return nil, validationError(ErrInvalidRequest, "time_expand", "unsupported expansion factor %d", factor)
	}
	out, err := resampleInterleaved(ctx, p, AudibleBatsOutputSampleRate*factor, maxBytes)
	if err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, wrapSentinel(ErrEmptyRange, nil, "time_expand", "no audio after time expansion", errors.CategoryAudio)
	}
	return &pcmBuffer{Data: out.Data, SampleRate: AudibleBatsOutputSampleRate, Channels: out.Channels, wavRoom: out.wavRoom}, nil
}
