package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestBirdNET_UpdateSettings_SyncsBothFields verifies updateSettings keeps the
// deprecated bn.Settings field and the lock-free settingsAtomic pointer pointing
// at the same snapshot. currentSettings() and the lock-guarded readers
// (NumSpecies/Labels/initialize*) rely on this invariant; if the two ever
// diverged, lock-free and lock-guarded readers would observe different settings,
// reintroducing the class of bug this branch set out to fix.
func TestBirdNET_UpdateSettings_SyncsBothFields(t *testing.T) {
	t.Parallel()

	bn := &BirdNET{}

	s1 := &conf.Settings{}
	bn.updateSettings(s1)
	assert.Same(t, s1, bn.Settings, "Settings field must point at the published snapshot")
	assert.Same(t, s1, bn.settingsAtomic.Load(), "atomic pointer must point at the published snapshot")

	// A subsequent update (e.g. a reload, or a rollback restoring the old
	// pointer) must move BOTH fields together.
	s2 := &conf.Settings{}
	bn.updateSettings(s2)
	assert.Same(t, s2, bn.Settings)
	assert.Same(t, s2, bn.settingsAtomic.Load())
}

// TestOrchestrator_UpdateSettings_SyncsBothFields is the Orchestrator counterpart
// of the BirdNET dual-write invariant.
func TestOrchestrator_UpdateSettings_SyncsBothFields(t *testing.T) {
	t.Parallel()

	o := &Orchestrator{}

	s1 := &conf.Settings{}
	o.updateSettings(s1)
	assert.Same(t, s1, o.Settings, "Settings field must point at the published snapshot")
	assert.Same(t, s1, o.settingsAtomic.Load(), "atomic pointer must point at the published snapshot")

	s2 := &conf.Settings{}
	o.updateSettings(s2)
	assert.Same(t, s2, o.Settings)
	assert.Same(t, s2, o.settingsAtomic.Load())
}

// TestBirdNET_CurrentSettings_PrefersAtomicOverDeprecatedField verifies that
// currentSettings() reads through the atomic pointer, not the raw bn.Settings
// field, when no global snapshot is published. This is the test-mode contract
// that lets struct-literal test fixtures (which set Settings but not the atomic)
// keep working while production code relies on the atomic for race-free reads.
func TestBirdNET_CurrentSettings_PrefersAtomicOverDeprecatedField(t *testing.T) {
	// Not parallel: temporarily clears the conf global snapshot, which is
	// process-wide. Restored on cleanup.
	saved := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(saved) })
	conf.SetTestSettings(nil)

	atomicSnapshot := &conf.Settings{}
	bn := &BirdNET{}
	bn.settingsAtomic.Store(atomicSnapshot)
	// Deliberately leave bn.Settings pointing elsewhere to prove the atomic wins.
	bn.Settings = &conf.Settings{}

	assert.Same(t, atomicSnapshot, bn.currentSettings(),
		"currentSettings must return the atomic snapshot when no global is published")
}
