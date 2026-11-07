package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestBSGBat_Wiring pins that BSG-BAT can be loaded at startup and rebuilt on a
// thread-count change: a registry entry without a loader is recognized but never
// runs, and one without a reload builder keeps its old thread count.
func TestBSGBat_Wiring(t *testing.T) {
	t.Parallel()

	assert.Contains(t, modelLoaders, RegistryIDBSGBat)
	assert.Contains(t, openvinoCapableSecondaryBuilders, RegistryIDBSGBat)
}

// TestBuildBSGBat_NotInstalled verifies that building with no configured files
// and nothing installed fails with a clear error, and queues no config repair.
func TestBuildBSGBat_NotInstalled(t *testing.T) {
	t.Parallel()

	o := &Orchestrator{}
	o.SetModelsDir(t.TempDir())

	_, _, err := o.buildBSGBat(&conf.Settings{}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed or configured")
	assert.Empty(t, o.pendingPathCorrections)
}
