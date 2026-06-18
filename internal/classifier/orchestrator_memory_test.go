package classifier

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// fakeInstance is a minimal ModelInstance for warm-up/RSS tests.
type fakeInstance struct {
	id         string
	sampleRate int
	clip       time.Duration
	predictedN int
	predictErr error
}

func (f *fakeInstance) Predict(_ context.Context, samples [][]float32) ([]datastore.Results, error) {
	if len(samples) > 0 {
		f.predictedN = len(samples[0])
	}
	return nil, f.predictErr
}
func (f *fakeInstance) Spec() ModelSpec {
	return ModelSpec{SampleRate: f.sampleRate, ClipLength: f.clip}
}
func (f *fakeInstance) ModelID() string      { return f.id }
func (f *fakeInstance) ModelName() string    { return f.id }
func (f *fakeInstance) ModelVersion() string { return "test" }
func (f *fakeInstance) NumSpecies() int      { return 1 }
func (f *fakeInstance) Labels() []string     { return nil }
func (f *fakeInstance) Close() error         { return nil }

func TestWarmupAndRecordRSS_RecordsNonNegativeDelta(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{modelRSS: make(map[string]int64)}
	inst := &fakeInstance{id: "Test_Model", sampleRate: 48000, clip: 3 * time.Second}

	before := o.captureRSSBefore()
	if before == 0 {
		t.Skip("process RSS unavailable on this platform")
	}
	o.warmupAndRecordRSS(inst.ModelID(), before, inst)

	// Warm-up must size the dummy clip from the spec (48000 * 3s = 144000).
	require.Equal(t, 144000, inst.predictedN, "warm-up dummy size")

	perModel, baseline := o.ModelRSS()
	require.Contains(t, perModel, inst.ModelID(), "expected modelRSS entry for Test_Model")
	assert.GreaterOrEqual(t, perModel[inst.ModelID()], int64(0), "RSS delta must be clamped to >= 0")
	assert.GreaterOrEqual(t, baseline, int64(0), "runtime baseline must be >= 0")
}

func TestModelRSS_ReturnsCopy(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{modelRSS: map[string]int64{"A": 10}}
	m, _ := o.ModelRSS()
	m["A"] = 999
	m2, _ := o.ModelRSS()
	assert.Equal(t, int64(10), m2["A"], "ModelRSS must return a copy; mutation of returned map must not affect the original")
}
