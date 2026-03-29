package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestNewOrchestrator_SyncsSharedState(t *testing.T) {
	t.Parallel()

	settings := conf.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })

	// Verify shared state is synced from primary model
	assert.Equal(t, o.primary.ModelInfo, o.ModelInfo, "ModelInfo should be synced")
	assert.NotNil(t, o.TaxonomyMap, "TaxonomyMap should be populated")
	assert.NotNil(t, o.ScientificIndex, "ScientificIndex should be populated")
	assert.Equal(t, settings, o.Settings, "Settings should be the same pointer")
}

func TestOrchestrator_PrimaryIsModelInstance(t *testing.T) {
	t.Parallel()

	settings := conf.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })

	// Verify primary model satisfies ModelInstance
	var mi ModelInstance = o.primary
	require.NotNil(t, mi)
	assert.NotEmpty(t, mi.ModelID())
	assert.NotEmpty(t, mi.ModelName())
	assert.NotEmpty(t, mi.ModelVersion())
	assert.Positive(t, mi.NumSpecies())
	assert.NotEmpty(t, mi.Labels())

	spec := mi.Spec()
	assert.Equal(t, 48000, spec.SampleRate)
	assert.Equal(t, 3*time.Second, spec.ClipLength)
}

func TestOrchestrator_ModelsMapPopulated(t *testing.T) {
	t.Parallel()

	settings := conf.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })

	assert.Len(t, o.models, 1, "Should have exactly one model in Phase 3b")
	entry, exists := o.models[o.ModelInfo.ID]
	require.True(t, exists, "Primary model should be registered by ID")
	assert.Equal(t, o.primary, entry.instance)
}
