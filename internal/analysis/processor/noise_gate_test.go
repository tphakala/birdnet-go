package processor

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

const (
	noiseGateSpace      = "noise-gate-test-space"
	noiseGateOtherSpace = "noise-gate-other-space"
	noiseGateDim        = 8
	noiseGateTolerance  = 1e-5

	// Similarities on either side of noisebank.MatchSimilarity.
	noiseGateBelow = 0.79
	noiseGateAbove = 0.81
)

// noiseGateExample is the one example the test banks hold: a unit vector on the
// first axis.
func noiseGateExample() []float32 {
	v := make([]float32, noiseGateDim)
	v[0] = 1
	return v
}

// noiseGateEmbedding returns a vector whose cosine similarity to the example is
// similarity.
func noiseGateEmbedding(similarity float64) []float32 {
	v := make([]float32, noiseGateDim)
	v[0] = float32(similarity)
	v[1] = float32(math.Sqrt(1 - similarity*similarity))
	return v
}

// noiseGateVector returns a vector with the three leading components given and
// zeros after them. (4, 3, 0) has a cosine of exactly 4/5 to the example.
func noiseGateVector(x, y, z float32) []float32 {
	v := make([]float32, noiseGateDim)
	v[0], v[1], v[2] = x, y, z
	return v
}

// noiseGateBank returns a bank holding the example in noiseGateSpace.
func noiseGateBank() *noisebank.Bank {
	b := noisebank.New()
	b.Add(noiseGateSpace, [][]float32{noiseGateExample()})
	return b
}

// TestApplyNoiseExampleGate pins when a window is tagged: only with a bank, an
// embedding of the example's space and dimension, and a similarity at or above
// the match threshold; then every detection of the window is tagged, with the
// similarity, whatever the model, and a tag the CV filter set is kept.
func TestApplyNoiseExampleGate(t *testing.T) {
	tests := []struct {
		name       string
		bank       *noisebank.Bank
		modelID    string
		embedding  []float32
		space      string
		preTagged  bool
		wantTagged bool
		wantSim    float64
	}{
		{"no bank", nil, classifier.RegistryIDBat, noiseGateEmbedding(1), noiseGateSpace, false, false, 0},
		{"window without embedding", noiseGateBank(), classifier.RegistryIDBat, nil, noiseGateSpace, false, false, 0},
		{"embedding of another space", noiseGateBank(), classifier.RegistryIDBat, noiseGateEmbedding(1), noiseGateOtherSpace, false, false, 0},
		{"embedding of another dimension", noiseGateBank(), classifier.RegistryIDBat, []float32{1, 0}, noiseGateSpace, false, false, 0},
		{"below the match threshold", noiseGateBank(), classifier.RegistryIDBat, noiseGateEmbedding(noiseGateBelow), noiseGateSpace, false, false, 0},
		{"above the match threshold", noiseGateBank(), classifier.RegistryIDBat, noiseGateEmbedding(noiseGateAbove), noiseGateSpace, false, true, noiseGateAbove},
		{"exactly at the match threshold", noiseGateBank(), classifier.RegistryIDBat, noiseGateVector(4, 3, 0), noiseGateSpace, false, true, 0.8},
		{"just below the match threshold", noiseGateBank(), classifier.RegistryIDBat, noiseGateVector(4, 3, 0.01), noiseGateSpace, false, false, 0},
		{"identical to the example", noiseGateBank(), classifier.RegistryIDBat, noiseGateExample(), noiseGateSpace, false, true, 1},
		{"a model outside the bat group", noiseGateBank(), classifier.RegistryIDBirdNETV24, noiseGateEmbedding(noiseGateAbove), noiseGateSpace, false, true, noiseGateAbove},
		{"a tag of the CV filter stays when nothing matches", noiseGateBank(), classifier.RegistryIDBat, noiseGateEmbedding(noiseGateBelow), noiseGateSpace, true, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Processor{}
			p.SetNoiseBank(tt.bank)
			item := classifier.Results{
				ModelID:        tt.modelID,
				Source:         datastore.AudioSource{ID: ultrasonicTestConnection},
				Embedding:      tt.embedding,
				EmbeddingSpace: tt.space,
			}
			detections := []Detections{{}, {}}
			if tt.preTagged {
				for i := range detections {
					detections[i].Result.Unlikely = true
					detections[i].Result.UnlikelySources = detection.UnlikelySourceUltrasonicCV
				}
			}

			p.applyNoiseExampleGate(&item, detections)

			for i := range detections {
				got := detections[i].Result
				noiseBit := got.UnlikelySources.Has(detection.UnlikelySourceNoiseExamples)
				assert.Equal(t, tt.wantTagged, noiseBit, "detection %d noise bit", i)
				assert.Equal(t, tt.wantTagged || tt.preTagged, got.Unlikely, "detection %d tag", i)
				assert.InDelta(t, tt.wantSim, got.NoiseSimilarity, noiseGateTolerance, "detection %d similarity", i)
			}
		})
	}
}

// noiseGateWindow describes one audio window of a detection for the composition
// test: how confident the hit was, whether the CV filter judges it flat, and the
// embedding it carries.
type noiseGateWindow struct {
	confidence float64
	flat       bool      // the CV filter runs and the window is a flat tone
	embedding  []float32 // nil for a model without embeddings
}

// TestApplyUnlikelyValidators_ComposeAcrossWindows pins the verdict of a pending
// detection built from several windows judged by the two validators: a window is
// judged when either validator tags it, the detection stays tagged only while every
// window was judged, each validator's figures come only from the windows it judged,
// and the result is the same whichever window arrives first.
func TestApplyUnlikelyValidators_ComposeAcrossWindows(t *testing.T) {
	const (
		cvOnlyConfidence = 0.7
		sourceBoth       = detection.UnlikelySourceUltrasonicCV | detection.UnlikelySourceNoiseExamples
		simHigh          = 0.95
		simLow           = 0.85
	)
	p, settings := newUltrasonicFilterFixture(t)
	p.SetNoiseBank(noiseGateBank())
	p.Settings = settings

	flatPCM := flatUltrasonicToneS16LE()
	cvOnly := func(confidence float64) noiseGateWindow {
		return noiseGateWindow{confidence: confidence, flat: true}
	}
	noise := func(confidence, similarity float64) noiseGateWindow {
		return noiseGateWindow{confidence: confidence, embedding: noiseGateEmbedding(similarity)}
	}
	both := func(confidence, similarity float64) noiseGateWindow {
		return noiseGateWindow{confidence: confidence, flat: true, embedding: noiseGateEmbedding(similarity)}
	}
	neither := func(confidence float64) noiseGateWindow {
		return noiseGateWindow{confidence: confidence, embedding: noiseGateEmbedding(noiseGateBelow)}
	}
	// No embedding and the CV filter off: nothing judges the window.
	unjudged := func(confidence float64) noiseGateWindow {
		return noiseGateWindow{confidence: confidence}
	}

	// judge runs both validators on one window as processResults does and returns the
	// detection it produced.
	judge := func(w noiseGateWindow) (*Detections, classifier.Results) {
		item := classifier.Results{
			ModelID:        classifier.RegistryIDBat,
			Source:         datastore.AudioSource{ID: ultrasonicTestConnection},
			Embedding:      w.embedding,
			EmbeddingSpace: noiseGateSpace,
		}
		cfg := *settings
		cfg.Bat.UltrasonicFilter.Enabled = w.flat
		if w.flat {
			item.PCMdata = flatPCM
		}
		det := detectionFor(mergeTestScientific, mergeTestCommon)
		det.Result.Confidence = w.confidence
		detections := []Detections{*det}
		p.applyUnlikelyValidators(&cfg, item, detections)
		return &detections[0], item
	}

	cvFigures, _ := judge(cvOnly(cvOnlyConfidence))
	require.True(t, cvFigures.Result.Unlikely, "the flat tone must be judged by the CV filter")
	cv, cvThreshold := cvFigures.Result.UltrasonicCV, cvFigures.Result.UltrasonicCVThreshold
	require.Positive(t, cvThreshold)

	tests := []struct {
		name        string
		windows     []noiseGateWindow
		wantTagged  bool
		wantSources detection.UnlikelySource
		wantCV      float64
		wantCVThr   float64
		wantSim     float64
	}{
		{"CV only", []noiseGateWindow{cvOnly(0.9)}, true, detection.UnlikelySourceUltrasonicCV, cv, cvThreshold, 0},
		{"noise only", []noiseGateWindow{noise(0.9, simLow)}, true, detection.UnlikelySourceNoiseExamples, 0, 0, simLow},
		{"both on one window", []noiseGateWindow{both(0.9, simLow)}, true, sourceBoth, cv, cvThreshold, simLow},
		{"one window each", []noiseGateWindow{cvOnly(0.8), noise(0.9, simLow)}, true, sourceBoth, cv, cvThreshold, simLow},
		{"lowest similarity wins", []noiseGateWindow{noise(0.8, simLow), noise(0.9, simHigh)}, true, detection.UnlikelySourceNoiseExamples, 0, 0, simLow},
		{"the CV window is the one with the best confidence", []noiseGateWindow{noise(0.7, simLow), cvOnly(0.95)}, true, sourceBoth, cv, cvThreshold, simLow},
		{"a window nothing judged clears the tag", []noiseGateWindow{cvOnly(0.9), unjudged(0.8)}, false, 0, 0, 0, 0},
		{"a window without a match clears the tag", []noiseGateWindow{noise(0.9, simHigh), neither(0.8)}, false, 0, 0, 0, 0},
		{"a window without embedding between noise windows clears the tag", []noiseGateWindow{noise(0.9, simHigh), unjudged(0.85), noise(0.8, simLow)}, false, 0, 0, 0, 0},
	}
	for _, tt := range tests {
		for _, order := range []struct {
			name    string
			reverse bool
		}{{"in order", false}, {"reversed", true}} {
			t.Run(tt.name+"/"+order.name, func(t *testing.T) {
				windows := slices.Clone(tt.windows)
				if order.reverse {
					slices.Reverse(windows)
				}
				p.pendingDetections = map[string]PendingDetection{}
				base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
				for i, w := range windows {
					det, item := judge(w)
					endAt := base.Add(time.Duration(i+1) * mergeTestClip)
					p.mergePendingHit(&item, det, endAt, mergeTestWindow)
				}

				got := onlyPending(t, p).Detection.Result
				assert.Equal(t, tt.wantTagged, got.Unlikely)
				assert.Equal(t, tt.wantSources, got.UnlikelySources)
				assert.InDelta(t, tt.wantCV, got.UltrasonicCV, noiseGateTolerance)
				assert.InDelta(t, tt.wantCVThr, got.UltrasonicCVThreshold, noiseGateTolerance)
				assert.InDelta(t, tt.wantSim, got.NoiseSimilarity, noiseGateTolerance)
			})
		}
	}
}

// TestApplyUnlikelyValidators_GateRunsForModelsOutsideTheBatGroup pins the dispatch:
// the CV filter is for bat-group models only, while the noise example gate judges
// any model whose chunk carries an embedding.
func TestApplyUnlikelyValidators_GateRunsForModelsOutsideTheBatGroup(t *testing.T) {
	p, settings := newUltrasonicFilterFixture(t)
	p.SetNoiseBank(noiseGateBank())

	for _, tt := range []struct {
		modelID string
		want    detection.UnlikelySource
	}{
		{classifier.RegistryIDBat, detection.UnlikelySourceUltrasonicCV | detection.UnlikelySourceNoiseExamples},
		{classifier.RegistryIDBirdNETV24, detection.UnlikelySourceNoiseExamples},
	} {
		t.Run(tt.modelID, func(t *testing.T) {
			item := classifier.Results{
				ModelID:        tt.modelID,
				PCMdata:        flatUltrasonicToneS16LE(),
				Source:         datastore.AudioSource{ID: ultrasonicTestConnection},
				Embedding:      noiseGateExample(),
				EmbeddingSpace: noiseGateSpace,
			}
			detections := []Detections{{}}
			p.applyUnlikelyValidators(settings, item, detections)
			assert.Equal(t, tt.want, detections[0].Result.UnlikelySources)
		})
	}
}

// TestShouldDiscardDetection_ReasonFollowsSources pins that the discard reason and
// so the aggregated discard counts name the validators that tagged the detection.
func TestShouldDiscardDetection_ReasonFollowsSources(t *testing.T) {
	const (
		cvSource    = detection.UnlikelySourceUltrasonicCV
		noiseSource = detection.UnlikelySourceNoiseExamples
	)
	p := &Processor{}
	settings := &conf.Settings{}
	settings.Bat.UltrasonicFilter = conf.UltrasonicFilterConfig{Enabled: true, DiscardUnlikely: true}

	tests := []struct {
		name    string
		sources detection.UnlikelySource
		want    string
	}{
		{"CV filter", cvSource, "ultrasonic filter"},
		{"noise examples", noiseSource, "noise examples"},
		{"both", cvSource | noiseSource, "ultrasonic filter and noise examples"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := PendingDetection{Source: mergeTestSource, Count: 1}
			item.Detection.Result.Unlikely = true
			item.Detection.Result.UnlikelySources = tt.sources

			discard, reason := p.shouldDiscardDetection(&item, settings, 1, nil, nil)
			assert.True(t, discard)
			assert.Equal(t, tt.want, reason)

			settings.Bat.UltrasonicFilter.DiscardUnlikely = false
			discard, _ = p.shouldDiscardDetection(&item, settings, 1, nil, nil)
			assert.False(t, discard, "Discard Unlikely off keeps a tagged detection, whoever tagged it")
			settings.Bat.UltrasonicFilter.DiscardUnlikely = true
		})
	}
}
