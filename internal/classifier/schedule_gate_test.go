package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestModelRegistry_ScheduleGatedIsExactlyBat pins the schedule-gated set to exactly
// the bat model and verifies isScheduleGated, the single predicate behind
// DefaultTargets, IsModelActive and ModelScheduleStatus. An unknown ID is never
// gated (the zero-value ModelInfo has scheduleGated false), so a custom model still
// analyzes.
func TestModelRegistry_ScheduleGatedIsExactlyBat(t *testing.T) {
	t.Parallel()

	var gated []string
	for id, info := range ModelRegistry {
		if info.scheduleGated {
			gated = append(gated, id)
		}
	}
	assert.ElementsMatch(t, []string{RegistryIDBat}, gated,
		"exactly the bat model is schedule-gated in the registry")

	assert.True(t, isScheduleGated(RegistryIDBat), "bat is schedule-gated")
	assert.False(t, isScheduleGated(RegistryIDBirdNETV24), "v2.4 is not schedule-gated")
	assert.False(t, isScheduleGated(RegistryIDPerchV2), "a loaded secondary is not schedule-gated")
	assert.False(t, isScheduleGated("not-a-model"), "an unknown ID is never schedule-gated")
}
