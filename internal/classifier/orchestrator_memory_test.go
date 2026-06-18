package classifier

import (
	"context"
	"testing"
	"time"

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
	o := &Orchestrator{modelRSS: make(map[string]int64)}
	inst := &fakeInstance{id: "Test_Model", sampleRate: 48000, clip: 3 * time.Second}

	before := o.captureRSSBefore()
	o.warmupAndRecordRSS(inst.ModelID(), before, inst)

	// Warm-up must size the dummy clip from the spec (48000 * 3s = 144000).
	if inst.predictedN != 144000 {
		t.Fatalf("warm-up dummy size = %d, want 144000", inst.predictedN)
	}
	perModel, baseline := o.ModelRSS()
	if _, ok := perModel[inst.ModelID()]; !ok {
		t.Fatal("expected modelRSS entry for Test_Model")
	}
	if perModel[inst.ModelID()] < 0 {
		t.Fatalf("RSS delta must be clamped to >= 0, got %d", perModel[inst.ModelID()])
	}
	if baseline < 0 {
		t.Fatalf("runtime baseline must be >= 0, got %d", baseline)
	}
}

func TestModelRSS_ReturnsCopy(t *testing.T) {
	o := &Orchestrator{modelRSS: map[string]int64{"A": 10}}
	m, _ := o.ModelRSS()
	m["A"] = 999
	m2, _ := o.ModelRSS()
	if m2["A"] != 10 {
		t.Fatalf("ModelRSS must return a copy; got mutated value %d", m2["A"])
	}
}
