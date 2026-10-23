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
		SourceCount:           2,
		ModelCount:            3, // differs from len(infos) and len(Models) so a wrong source fails
		Models:                []cadence.ModelCost{{ModelID: v24.ID, Latency: 166 * time.Millisecond}},
	}

	got := buildAnalysisCadence(plan, infos, s)
	require.NotNil(t, got)
	assert.Equal(t, "capped", got.Status)
	assert.Equal(t, 5, got.FilterLevel, "the level the confirmations were computed for")
	assert.InDelta(t, 2.8, got.ConfiguredOverlapSec, 1e-9)
	assert.InDelta(t, 1.8, got.EffectiveOverlapSec, 1e-9)
	assert.Equal(t, int64(1200), got.MinBaseStepMs)
	assert.InDelta(t, 0.83, got.EstimatedDutyConfigured, 1e-9)
	assert.InDelta(t, 0.70, got.EstimatedDutyEffective, 1e-9)
	assert.InDelta(t, 0.75, got.DutyCeiling, 1e-9)
	assert.Equal(t, 2, got.SourceCount)
	assert.Equal(t, 3, got.ModelCount)
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
	assert.Contains(t, string(b), `"filterLevel":0`, "level 0 is sent, not omitted")
}

func TestBuildAnalysisCadence_ListsUnknownLatencyModels(t *testing.T) {
	t.Parallel()
	plan := &cadence.Plan{Status: cadence.StatusOK, UnknownLatencyModels: []string{"Perch_V2"}}
	got := buildAnalysisCadence(plan, nil, &conf.Settings{})
	require.NotNil(t, got)
	assert.Equal(t, []string{"Perch_V2"}, got.UnknownLatencyModels)
}

// TestBuildAnalysisCadence_PerModelCadence pins the per-model rows for a mixed
// model set given out of order: the 5 s Perch step scales with the effective
// overlap, the bat step stays at half its clip, each model gets its own latency,
// and rows are sorted by ID.
func TestBuildAnalysisCadence_PerModelCadence(t *testing.T) {
	t.Parallel()
	v24 := classifier.ModelRegistry[classifier.RegistryIDBirdNETV24]
	perch := classifier.ModelRegistry[classifier.RegistryIDPerchV2]
	bat := classifier.ModelRegistry[classifier.RegistryIDBat]
	infos := []classifier.ModelInfo{perch, bat, v24}
	s := &conf.Settings{}
	s.BirdNET.Overlap = 2.8
	s.Realtime.FalsePositiveFilter.Level = 5
	plan := &cadence.Plan{
		ConfiguredBaseOverlap: 2800 * time.Millisecond,
		EffectiveBaseOverlap:  1800 * time.Millisecond,
		Status:                cadence.StatusCapped,
		Models: []cadence.ModelCost{
			{ModelID: v24.ID, Latency: 166 * time.Millisecond},
			{ModelID: perch.ID, Latency: 900 * time.Millisecond},
			{ModelID: bat.ID, Latency: 40 * time.Millisecond},
		},
	}

	got := buildAnalysisCadence(plan, infos, s)
	require.NotNil(t, got)
	require.Len(t, got.Models, 3)
	ids := []string{got.Models[0].ID, got.Models[1].ID, got.Models[2].ID}
	assert.Equal(t, []string{"Bat", "BirdNET_V2.4", "Perch_V2"}, ids, "rows sorted by ID")

	byID := map[string]CadenceModelInfo{}
	for _, m := range got.Models {
		byID[m.ID] = m
	}
	assert.Equal(t, int64(1500), byID["Bat"].StepMs, "bat stays at half its 3 s clip")
	assert.Equal(t, int64(40), byID["Bat"].ProbeLatencyMs)
	assert.Equal(t, 1, byID["Bat"].Confirmations, "bat filter level 0")
	assert.Equal(t, int64(1200), byID["BirdNET_V2.4"].StepMs)
	assert.Equal(t, int64(166), byID["BirdNET_V2.4"].ProbeLatencyMs)
	assert.Equal(t, int64(2000), byID["Perch_V2"].StepMs, "5 s clip at 1.8/3 of its length overlapped")
	assert.Equal(t, int64(900), byID["Perch_V2"].ProbeLatencyMs)
	assert.Equal(t, 3, byID["Perch_V2"].Confirmations, "level 5 over 3 windows of 2 s")
	assert.InDelta(t, 3.0, byID["Perch_V2"].WindowsInReference, 1e-9)
}
