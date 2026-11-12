package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
)

// idleEmbedder is an embedder that is never asked to embed anything.
type idleEmbedder struct{}

func (idleEmbedder) PredictModelTimed(context.Context, string, [][]float32) (classifier.Prediction, error) {
	return classifier.Prediction{}, nil
}
func (idleEmbedder) ModelSpecFor(string) (classifier.ModelSpec, bool) {
	return classifier.ModelSpec{}, false
}
func (idleEmbedder) EffectiveBaseOverlap() time.Duration     { return 0 }
func (idleEmbedder) EmbeddingSpaceFor(string) (string, bool) { return "", false }

// TestStartNoiseExamples_OnlyInV2OnlyMode: a v2 manager alone (the migration
// window) must not start the service; only the v2-only mode does, and it hands the
// same service to the processor.
func TestStartNoiseExamples_OnlyInV2OnlyMode(t *testing.T) {
	t.Parallel()

	migration := newPrivacyRollupTestService(t, false)
	migration.startNoiseExamples(t.Context(), idleEmbedder{})
	assert.Nil(t, migration.noiseExamples, "a v2 manager outside v2-only mode gets no service")
	assert.Nil(t, migration.proc.NoiseExamples())

	v2Only := newPrivacyRollupTestService(t, true)
	v2Only.startNoiseExamples(t.Context(), idleEmbedder{})
	require.NotNil(t, v2Only.noiseExamples)
	assert.Same(t, v2Only.noiseExamples, v2Only.proc.NoiseExamples(), "the API reaches the started service through the processor")
	t.Cleanup(func() { require.NoError(t, v2Only.stopNoiseExamples(context.WithoutCancel(t.Context()))) })

	noDB := &APIServerService{}
	noDB.startNoiseExamples(t.Context(), idleEmbedder{})
	assert.Nil(t, noDB.noiseExamples)

	noEmbedder := newPrivacyRollupTestService(t, true)
	noEmbedder.startNoiseExamples(t.Context(), nil)
	assert.Nil(t, noEmbedder.noiseExamples, "without a model orchestrator there is nothing to learn with")
}

// TestStartNoiseExamples_LoadFailureLeavesTheFeatureOff: when the stored examples
// cannot be read the service is not published, so the gate stays off and the API
// reports the feature unavailable.
func TestStartNoiseExamples_LoadFailureLeavesTheFeatureOff(t *testing.T) {
	t.Parallel()

	svc := newPrivacyRollupTestService(t, true)
	require.NoError(t, svc.dbService.V2Manager().DB().Migrator().DropTable("noise_examples"))

	svc.startNoiseExamples(t.Context(), idleEmbedder{})

	assert.Nil(t, svc.noiseExamples)
	assert.Nil(t, svc.proc.NoiseExamples())
	require.NoError(t, svc.stopNoiseExamples(t.Context()), "stopping an unpublished service is a no-op")
}
