package transcode

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	batSourceRate = 256000
	batToneHz     = 40000.0
	batSeconds    = 0.25
	// expandLengthSlack is how many frames the expanded length may differ from
	// the exact ratio (resampler latency).
	expandLengthSlack = 4096
)

func TestTimeExpand_FactorsShiftPitchAndStretch(t *testing.T) {
	t.Parallel()

	for _, rate := range []int{96000, 256000, 768000, maxSourceSampleRate} {
		for _, factor := range []int{5, 10, 16, 20} {
			t.Run(fmt.Sprintf("%dHz_x%d", rate, factor), func(t *testing.T) {
				t.Parallel()
				src := tone(rate, 1, batSeconds, batToneHz, testAmp)
				in := newPCM(src, rate, 1)

				out, err := timeExpand(t.Context(), in, factor, maxDecodedPCMBytes)
				require.NoError(t, err)

				assert.Equal(t, AudibleBatsOutputSampleRate, out.SampleRate)
				assert.Equal(t, 1, out.Channels)
				wantFrames := len(src) * AudibleBatsOutputSampleRate * factor / rate
				assert.InDelta(t, wantFrames, out.Frames(), expandLengthSlack)

				want := batToneHz / float64(factor)
				x := channelOf(bytesInt16(out.Data), 1, 0)
				assert.Truef(t, dominantIs(x, AudibleBatsOutputSampleRate, want, want*1.7), "expect %v Hz to dominate", want)
			})
		}
	}
}

func TestTimeExpand_RelabelWhenSourceAlreadyAtTargetRate(t *testing.T) {
	t.Parallel()

	const factor = 16
	rate := AudibleBatsOutputSampleRate * factor
	src := tone(rate, 1, batSeconds, batToneHz, testAmp)
	out, err := timeExpand(t.Context(), newPCM(src, rate, 1), factor, maxDecodedPCMBytes)
	require.NoError(t, err)
	assert.Equal(t, len(src), out.Frames(), "a source at 48000*factor is a pure relabel")
	assert.Equal(t, int16Bytes(src), out.Data)
	assert.Equal(t, AudibleBatsOutputSampleRate, out.SampleRate)
}

func TestTimeExpand_StereoPerChannel(t *testing.T) {
	t.Parallel()

	const (
		rate   = 256000
		factor = 10
		leftHz = 40000.0
		rghtHz = 30000.0
	)
	left := tone(rate, 1, batSeconds, leftHz, testAmp)
	right := tone(rate, 1, batSeconds, rghtHz, testAmp)
	inter := make([]int16, 2*len(left))
	for i := range left {
		inter[2*i], inter[2*i+1] = left[i], right[i]
	}
	out, err := timeExpand(t.Context(), newPCM(inter, rate, 2), factor, maxDecodedPCMBytes)
	require.NoError(t, err)
	require.Equal(t, 2, out.Channels)

	got := bytesInt16(out.Data)
	l, r := channelOf(got, 2, 0), channelOf(got, 2, 1)
	assert.True(t, dominantIs(l, AudibleBatsOutputSampleRate, leftHz/factor, leftHz/factor*1.7))
	assert.True(t, dominantIs(r, AudibleBatsOutputSampleRate, rghtHz/factor, rghtHz/factor*1.7))
}

func TestTimeExpand_OutputOverCap(t *testing.T) {
	t.Parallel()

	src := tone(256000, 1, batSeconds, batToneHz, testAmp)
	_, err := timeExpand(t.Context(), newPCM(src, 256000, 1), 20, 1<<18)
	require.ErrorIs(t, err, ErrAudioTooLong)
}

func TestTimeExpand_InvalidFactor(t *testing.T) {
	t.Parallel()

	src := tone(256000, 1, 0.05, batToneHz, testAmp)
	for _, factor := range []int{0, 1, 4, 6, 15, 21, -10} {
		_, err := timeExpand(t.Context(), newPCM(src, 256000, 1), factor, maxDecodedPCMBytes)
		require.ErrorIsf(t, err, ErrInvalidRequest, "factor %d", factor)
	}
}

func TestTimeExpand_Cancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	src := tone(256000, 1, batSeconds, batToneHz, testAmp)
	_, err := timeExpand(ctx, newPCM(src, 256000, 1), 10, maxDecodedPCMBytes)
	require.ErrorIs(t, err, context.Canceled)
}

// Normalization must measure what is played. A low tone is attenuated by the
// loudness meter's high-pass once it has been slowed down, but not before, so a
// wrong order lands the result well off the target.
func TestAudibleBats_NormalizesAfterExpansion(t *testing.T) {
	t.Parallel()

	const (
		lowToneHz = 500.0
		factor    = 10
	)
	path := filepath.Join(t.TempDir(), "bat.wav")
	writeWAVFile(t, path, tone(batSourceRate, 1, 4, lowToneHz, 0.05), batSourceRate)

	data, err := AudibleBats(t.Context(), path, factor, Filters{Normalize: true})
	require.NoError(t, err)
	got := decodeBytes(t, data, ".wav")
	assert.InDelta(t, normalizeTargetLUFS, lufsOf(t, got).IntegratedLUFS, normalizeToleranceLU)
}
