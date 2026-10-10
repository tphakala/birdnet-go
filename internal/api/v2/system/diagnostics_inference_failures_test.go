package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/classifier"
)

// TestBuildInferenceFailuresProvider pins the provider's wiring guards: no
// processor or no orchestrator reports no models (the check then skips), and an
// orchestrator with no loaded model reports an empty, non-nil set.
func TestBuildInferenceFailuresProvider(t *testing.T) {
	_, h := setupSystemTestEnvironment(t)
	assert.Nil(t, h.buildInferenceFailuresProvider()(), "no processor")

	h.Processor = &processor.Processor{}
	assert.Nil(t, h.buildInferenceFailuresProvider()(), "no orchestrator")

	h.Processor = &processor.Processor{Bn: &classifier.Orchestrator{}}
	got := h.buildInferenceFailuresProvider()()
	require.NotNil(t, got)
	assert.Empty(t, got, "no loaded model")
}
