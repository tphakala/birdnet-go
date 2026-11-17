package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWireClipDenoiseRecorder_OnlyInV2OnlyMode: a v2 manager alone (the migration
// window) must not hand the processor a recorder; only the v2-only mode does.
func TestWireClipDenoiseRecorder_OnlyInV2OnlyMode(t *testing.T) {
	t.Parallel()

	migration := newPrivacyRollupTestService(t, false)
	migration.wireClipDenoiseRecorder()
	assert.Nil(t, migration.proc.ClipDenoiseRecorder())

	v2Only := newPrivacyRollupTestService(t, true)
	v2Only.wireClipDenoiseRecorder()
	assert.NotNil(t, v2Only.proc.ClipDenoiseRecorder())

	noDB := &APIServerService{}
	noDB.wireClipDenoiseRecorder()
	assert.Nil(t, noDB.proc)
}
