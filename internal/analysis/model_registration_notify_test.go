package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestModelNotRegisteredKey pins the suppression-map key format and the collision-fix
// invariant: the key is composed from the SOURCE ID (not the display name), so two sources
// that share a display name (and therefore the same model string) but differ by ID produce
// distinct keys. notifyModelsNotRegistered (writer) and clearModelNotRegistered (prefix
// matcher) both build keys from this one helper, so a key-format divergence between them is
// impossible by construction.
func TestModelNotRegisteredKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "src-a\x00perch_v2", modelNotRegisteredKey("src-a", "perch_v2"),
		"key is sourceID + NUL + models")
	assert.NotEqual(t, modelNotRegisteredKey("src-a", "perch_v2"), modelNotRegisteredKey("src-b", "perch_v2"),
		"distinct source IDs must not share a suppression key even when the model string matches")
}

// TestClearModelNotRegistered verifies the recovery/removal clear drops only the target
// source's suppression entries. Keys are built through modelNotRegisteredKey (the same
// writer notifyModelsNotRegistered uses), so the test round-trips the production key format
// rather than a hand-written copy of it. Two sources sharing a display name (a real
// possibility the runtime tolerates) must not clear each other's window.
func TestClearModelNotRegistered(t *testing.T) {
	// Not parallel: mutates the package-global modelNotRegisteredSeen.
	const (
		sidA = "src-aaaa"
		sidB = "src-bbbb"
	)
	// Both sources carry the same display-name-derived model string; only the source-ID
	// prefix distinguishes their keys.
	keyA := modelNotRegisteredKey(sidA, "perch_v2")
	keyB := modelNotRegisteredKey(sidB, "perch_v2")
	now := time.Now()
	modelNotRegisteredSeen.Store(keyA, now)
	modelNotRegisteredSeen.Store(keyB, now)
	t.Cleanup(func() {
		modelNotRegisteredSeen.Delete(keyA)
		modelNotRegisteredSeen.Delete(keyB)
	})

	clearModelNotRegistered(sidA)

	_, aPresent := modelNotRegisteredSeen.Load(keyA)
	_, bPresent := modelNotRegisteredSeen.Load(keyB)
	assert.False(t, aPresent, "the cleared source's suppression entry must be removed")
	assert.True(t, bPresent, "another source's entry (even with an identical model string) must survive")
}

// TestClearModelNotRegistered_PrefixIsExactSegment guards the NUL delimiter's load-bearing
// role: a source ID that is a plain string prefix of another ("src" vs "src-2") must NOT
// clear the longer one, because the delimiter makes the ID an exact key segment rather than
// a raw string prefix.
func TestClearModelNotRegistered_PrefixIsExactSegment(t *testing.T) {
	const (
		sidShort = "src"
		sidLong  = "src-2"
	)
	kShort := modelNotRegisteredKey(sidShort, "perch_v2")
	kLong := modelNotRegisteredKey(sidLong, "perch_v2")
	now := time.Now()
	modelNotRegisteredSeen.Store(kShort, now)
	modelNotRegisteredSeen.Store(kLong, now)
	t.Cleanup(func() {
		modelNotRegisteredSeen.Delete(kShort)
		modelNotRegisteredSeen.Delete(kLong)
	})

	clearModelNotRegistered(sidShort)

	_, shortPresent := modelNotRegisteredSeen.Load(kShort)
	_, longPresent := modelNotRegisteredSeen.Load(kLong)
	assert.False(t, shortPresent, "the exact source's entry is cleared")
	assert.True(t, longPresent, "a source whose ID merely shares a string prefix must NOT be cleared")
}

// TestClearModelNotRegistered_MultipleModelSets clears every entry for a source regardless
// of which model set was previously reported: the recovery path does not know the prior
// unregistered set, so it clears by source-ID prefix.
func TestClearModelNotRegistered_MultipleModelSets(t *testing.T) {
	const sid = "src-multi"
	k1 := modelNotRegisteredKey(sid, "perch_v2")
	k2 := modelNotRegisteredKey(sid, "perch_v2, bat")
	now := time.Now()
	modelNotRegisteredSeen.Store(k1, now)
	modelNotRegisteredSeen.Store(k2, now)
	t.Cleanup(func() {
		modelNotRegisteredSeen.Delete(k1)
		modelNotRegisteredSeen.Delete(k2)
	})

	clearModelNotRegistered(sid)

	_, p1 := modelNotRegisteredSeen.Load(k1)
	_, p2 := modelNotRegisteredSeen.Load(k2)
	assert.False(t, p1, "all suppression entries for the source must be cleared")
	assert.False(t, p2, "all suppression entries for the source must be cleared")
}
