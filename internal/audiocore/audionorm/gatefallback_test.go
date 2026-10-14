package audionorm

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
)

const (
	gateTestRate     = 48000
	gateTestTarget   = -23.0
	gateTestCeiling  = -2.0
	gateTestMaxGain  = 60.0
	gateTestSeconds  = 5.0
	gateTestToneHz   = 1000.0
	subGateToneDBFS  = -80.0
	audibleToneDBFS  = -30.0
	targetToleranceL = 1.0
)

func gateTestOptions() Options {
	return Options{SampleRate: gateTestRate, Channels: 1, TargetLUFS: gateTestTarget, TruePeakDBTP: gateTestCeiling}
}

func TestGateFallbackGainDB_LiftIsMinOfPeakHeadroomAndGateDistance(t *testing.T) {
	t.Parallel()

	negInf := math.Inf(-1)
	tests := []struct {
		name   string
		meas   Measurement
		want   float64
		wantOK bool
	}{
		{name: "gate distance binds for a very quiet peak", meas: Measurement{IntegratedLUFS: negInf, TruePeakDBTP: -60}, want: gateTestTarget - AbsoluteGateLUFS, wantOK: true},
		{name: "peak headroom binds for a louder peak", meas: Measurement{IntegratedLUFS: negInf, TruePeakDBTP: -30}, want: gateTestCeiling - (-30), wantOK: true},
		{name: "true silence is left alone", meas: Measurement{IntegratedLUFS: negInf, TruePeakDBTP: negInf}, want: 0, wantOK: false},
		{name: "a measurable clip needs no fallback", meas: Measurement{IntegratedLUFS: -30, TruePeakDBTP: -10}, want: 0, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := GateFallbackGainDB(tt.meas, gateTestTarget, gateTestCeiling)
			assert.Equal(t, tt.wantOK, ok)
			assert.InDelta(t, tt.want, got, 1e-9)
		})
	}
}

func TestRefineLiftedGainDB_LandsLiftedClipOnTarget(t *testing.T) {
	t.Parallel()

	opts := gateTestOptions()
	pcm := int16sToLEBytes(sineInt16(subGateToneDBFS, gateTestToneHz, gateTestSeconds, gateTestRate))
	meas, err := MeasureInt16Bytes(pcm, gateTestRate, 1)
	require.NoError(t, err)
	require.True(t, math.IsInf(meas.IntegratedLUFS, -1), "fixture must sit under the absolute gate")

	lift, ok := GateFallbackGainDB(meas, gateTestTarget, gateTestCeiling)
	require.True(t, ok)
	extra := RefineLiftedGainDB(pcm, lift, opts)
	assert.NotZero(t, extra, "a lifted clip that becomes measurable gets a refinement")

	got, err := MeasureInt16Bytes(pcmgain.Applied(pcm, lift+extra), gateTestRate, 1)
	require.NoError(t, err)
	assert.InDelta(t, gateTestTarget, got.IntegratedLUFS, targetToleranceL)
}

func TestRefineLiftedGainDB_StillSubGateOrErrorYieldsZero(t *testing.T) {
	t.Parallel()

	pcm := int16sToLEBytes(sineInt16(subGateToneDBFS, gateTestToneHz, gateTestSeconds, gateTestRate))
	assert.Zero(t, RefineLiftedGainDB(pcm, 0, gateTestOptions()), "no lift leaves the clip under the gate")
	bad := gateTestOptions()
	bad.SampleRate = 100
	assert.Zero(t, RefineLiftedGainDB(pcm, 10, bad), "a measurement error yields no refinement")
}

func TestPlanGainWithGateFallbackInt16Bytes(t *testing.T) {
	t.Parallel()

	opts := gateTestOptions()

	t.Run("sub-gate clip is lifted to target", func(t *testing.T) {
		t.Parallel()
		pcm := int16sToLEBytes(sineInt16(subGateToneDBFS, gateTestToneHz, gateTestSeconds, gateTestRate))
		plan, err := PlanGainWithGateFallbackInt16Bytes(pcm, opts, gateTestMaxGain)
		require.NoError(t, err)
		assert.True(t, plan.GateLifted)
		assert.False(t, plan.Clamped)
		got, err := MeasureInt16Bytes(pcmgain.Applied(pcm, plan.GainDB), gateTestRate, 1)
		require.NoError(t, err)
		assert.InDelta(t, gateTestTarget, got.IntegratedLUFS, targetToleranceL)
	})

	t.Run("measurable clip matches the plain plan", func(t *testing.T) {
		t.Parallel()
		pcm := int16sToLEBytes(sineInt16(audibleToneDBFS, gateTestToneHz, gateTestSeconds, gateTestRate))
		plan, err := PlanGainWithGateFallbackInt16Bytes(pcm, opts, gateTestMaxGain)
		require.NoError(t, err)
		assert.False(t, plan.GateLifted)
		assert.False(t, plan.Clamped)
		want, wantMeas, wantRes, _, err := PlanClampedGainInt16Bytes(pcm, opts, gateTestMaxGain)
		require.NoError(t, err)
		assert.InDelta(t, want, plan.GainDB, 1e-9)
		assert.Equal(t, wantMeas, plan.Measurement)
		assert.Equal(t, wantRes, plan.Result)
	})

	t.Run("silence is left alone", func(t *testing.T) {
		t.Parallel()
		pcm := make([]byte, gateTestRate*2*int(gateTestSeconds))
		plan, err := PlanGainWithGateFallbackInt16Bytes(pcm, opts, gateTestMaxGain)
		require.NoError(t, err)
		assert.False(t, plan.GateLifted)
		assert.Zero(t, plan.GainDB)
	})

	t.Run("clamp is reported", func(t *testing.T) {
		t.Parallel()
		pcm := int16sToLEBytes(sineInt16(audibleToneDBFS, gateTestToneHz, gateTestSeconds, gateTestRate))
		plan, err := PlanGainWithGateFallbackInt16Bytes(pcm, opts, 1)
		require.NoError(t, err)
		assert.True(t, plan.Clamped)
		assert.InDelta(t, 1.0, math.Abs(plan.GainDB), 1e-9)
	})

	t.Run("measurement error is returned", func(t *testing.T) {
		t.Parallel()
		bad := opts
		bad.SampleRate = 100
		_, err := PlanGainWithGateFallbackInt16Bytes(make([]byte, 400), bad, gateTestMaxGain)
		require.Error(t, err)
	})
}
