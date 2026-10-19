package processor

import (
	"bytes"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
)

// Bytes the property test writes into audio that is not real: the zero-filled
// overlap head of a spliced window. Real audio is zero (or one for speech).
const propFakeByte = 0x7f

// recordingStreamer logs the real audio it was given, per stream instance, so
// the test can compare the gate's published spans against what was scored.
type recordingStreamer struct {
	curEnd *time.Time
	scored []audioSpan
}

func (r *recordingStreamer) Append(pcm []byte, rate int) error {
	end := *r.curEnd
	start := end.Add(-pcmChunkDuration(len(pcm), rate))
	sampleDur := time.Second / time.Duration(rate)
	runStart := -1
	flush := func(i int) {
		if runStart >= 0 {
			r.scored = append(r.scored, audioSpan{
				from: start.Add(time.Duration(runStart) * sampleDur),
				to:   start.Add(time.Duration(i) * sampleDur),
			})
			runStart = -1
		}
	}
	for i := 0; i < len(pcm); i += vadBytesPerSample * 64 { // 64-sample resolution is enough
		if pcm[i] == propFakeByte {
			flush(i / vadBytesPerSample)
		} else if runStart < 0 {
			runStart = i / vadBytesPerSample
		}
	}
	flush(len(pcm) / vadBytesPerSample)
	return nil
}

func (r *recordingStreamer) Flush(vad.SpeechSession) (prob float32, ok bool, framesRun int, err error) {
	return 0.5, true, 1, nil
}

func (r *recordingStreamer) Reset() { r.scored = nil }

// largestHole returns the longest stretch of [span.from, span.to] not covered by
// the union of the logged intervals.
func largestHole(span audioSpan, logged []audioSpan) time.Duration {
	sorted := slices.Clone(logged)
	slices.SortFunc(sorted, func(a, b audioSpan) int { return a.from.Compare(b.from) })
	cursor := span.from
	var hole time.Duration
	for _, iv := range sorted {
		if iv.from.After(cursor) {
			if iv.from.After(span.to) {
				break
			}
			hole = max(hole, iv.from.Sub(cursor))
		}
		if iv.to.After(cursor) {
			cursor = iv.to
		}
	}
	if span.to.After(cursor) {
		hole = max(hole, span.to.Sub(cursor))
	}
	return hole
}

// TestPrivacy_CoverageNeverClaimsUnscoredAudio generates seeded random sequences
// mixing a 3 s and a 5 s model at random steps with queue drops, producer gaps,
// a model stopping and returning, a PreCapture change and a VAD toggle. After
// every item it asserts that no published VAD span contains an unscored hole
// larger than the join tolerance (I3), and that the per-source hit time never
// decreases (I2).
func TestPrivacy_CoverageNeverClaimsUnscoredAudio(t *testing.T) {
	stepsMs := []int{200, 300, 600, 1000, 333, 1667, 3000, 5000}
	models := []string{testVADModel, testVADModelB}
	zeroLen := 0
	for _, id := range models {
		spec, ok := classifier.GetModelSpec(id)
		require.True(t, ok)
		zeroLen = max(zeroLen, int(spec.ClipLength.Seconds())*spec.EffectiveSampleRate()*vadBytesPerSample)
	}
	zero := make([]byte, zeroLen)

	for seed := uint64(1); seed <= 100; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed*7919)) //nolint:gosec // G404: deterministic test sequence
		base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
		var curEnd time.Time
		streams := map[vadStreamKey]*recordingStreamer{}
		g := newTestGate(nil, &fakeSession{}, nil)
		var lastKey vadStreamKey
		g.newStreamer = func() vad.Streamer {
			rs := &recordingStreamer{curEnd: &curEnd}
			streams[lastKey] = rs
			return rs
		}
		p := &Processor{LastHumanDetection: map[string]HumanDetection{}, vadGate: g}
		settings := vadSettings(true, 0.35)
		settings.Realtime.PrivacyFilter.Confidence = 0.05

		ends := []time.Time{base.Add(10 * time.Second), base.Add(10 * time.Second)}
		var prevHit time.Time
		active := []bool{true, true}
		for op := range 60 {
			m := rng.IntN(len(models))
			switch rng.IntN(20) {
			case 0:
				active[m] = !active[m] // model stops or returns
			case 1:
				settings.Realtime.Audio.Export.PreCapture = rng.IntN(6)
			case 2:
				settings.Realtime.PrivacyFilter.VAD.Enabled = !settings.Realtime.PrivacyFilter.VAD.Enabled
			}
			if !active[m] {
				continue
			}
			step := time.Duration(stepsMs[rng.IntN(len(stepsMs))]) * time.Millisecond
			if rng.IntN(8) == 0 {
				step *= time.Duration(1 + rng.IntN(4)) // queue drops widen the step
			}
			ends[m] = ends[m].Add(step)
			gap := rng.IntN(10) == 0
			curEnd = ends[m]

			spec := classifier.ModelRegistry[models[m]].Spec
			pcm := zero[:int(spec.ClipLength.Seconds())*spec.EffectiveSampleRate()*vadBytesPerSample]
			if gap {
				pcm = bytes.Clone(pcm)
				head := len(pcm) / 2
				for i := range head {
					pcm[i] = propFakeByte
				}
			}
			item := classifier.Results{
				ModelID: models[m],
				PCMdata: pcm,
				Audio:   classifier.AudioTiming{EndAt: ends[m], Gap: gap},
			}
			item.Source = datastore.AudioSource{ID: "src"}
			lastKey = vadStreamKey{source: "src", model: models[m]}

			if rng.IntN(5) == 0 { // a label hit with an unrelated stamp
				label := item
				label.Audio.EndAt = base.Add(time.Duration(rng.IntN(120)) * time.Second)
				p.handleHumanDetection(settings, label, datastore.Results{Species: "Speech", Confidence: 0.9})
			}

			span, ran := runGate(p, settings, &item)
			if ran {
				rs := streams[lastKey]
				require.NotNil(t, rs)
				hole := largestHole(span, rs.scored)
				require.LessOrEqualf(t, hole, vadCoverageJoinTolerance+10*time.Millisecond,
					"seed %d op %d: span %v..%v has an unscored hole of %v", seed, op,
					span.from.Sub(base), span.to.Sub(base), hole)
				assert.False(t, span.to.Before(span.from))
			}
			cur := p.LastHumanDetection["src"].Time
			assert.Falsef(t, cur.Before(prevHit), "seed %d op %d: hit time moved backward", seed, op)
			prevHit = cur
		}
	}
}
