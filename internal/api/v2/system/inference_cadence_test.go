package system

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestBuildAnalysisCadence_NilPlanOmitsField(t *testing.T) {
	t.Parallel()
	assert.Nil(t, buildAnalysisCadence(nil, nil, &conf.Settings{}))

	b, err := json.Marshal(InferenceStatusResponse{})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "analysisCadence")
}

func TestBuildAnalysisCadence_UsesEffectiveOverlap(t *testing.T) {
	t.Parallel()
	v24 := classifier.ModelRegistry[classifier.RegistryIDBirdNETV24]
	infos := []classifier.ModelInfo{v24}
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.8
	s.Realtime.FalsePositiveFilter.Level = 5
	plan := &cadence.Plan{
		ConfiguredBaseOverlap: 2800 * time.Millisecond,
		EffectiveBaseOverlap:  1800 * time.Millisecond,
		MinBaseStep:           1200 * time.Millisecond,
		Status:                cadence.StatusCapped,
		DutyAtConfigured:      0.83,
		DutyAtEffective:       0.70,
		DutyCeiling:           0.75,
		SourceCount:           1,
		ModelCount:            1,
		Models:                []cadence.ModelCost{{ModelID: v24.ID, Latency: 166 * time.Millisecond}},
	}

	got := buildAnalysisCadence(plan, infos, s)
	require.NotNil(t, got)
	assert.Equal(t, "capped", got.Status)
	assert.InDelta(t, 2.8, got.ConfiguredOverlapSec, 1e-9)
	assert.InDelta(t, 1.8, got.EffectiveOverlapSec, 1e-9)
	assert.Equal(t, int64(1200), got.MinBaseStepMs)
	require.Len(t, got.Models, 1)
	m := got.Models[0]
	assert.Equal(t, int64(1200), m.StepMs)
	assert.Equal(t, int64(166), m.ProbeLatencyMs)
	assert.Equal(t, 4, m.Confirmations, "level 5 at the effective 1.8 s, not 21 at the configured 2.8 s")
	assert.InDelta(t, 5.0, m.WindowsInReference, 1e-9)
}

func TestBuildAnalysisCadence_UnknownLatencyMarshalsAsEmptyArray(t *testing.T) {
	t.Parallel()
	plan := &cadence.Plan{Status: cadence.StatusOK}
	got := buildAnalysisCadence(plan, nil, &conf.Settings{})
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"unknownLatencyModels":[]`)
	assert.Contains(t, string(b), `"models":[]`)
}
