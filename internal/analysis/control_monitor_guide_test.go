package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGuideProviderSetChanged covers the decision that drives species-guide cache
// invalidation on reconfigure: whether the registered provider set (determined solely
// by EnableWikipedia, since OpenFauna is always present) is changing relative to the
// set that produced the currently-cached guides.
func TestGuideProviderSetChanged(t *testing.T) {
	t.Parallel()

	bp := func(b bool) *bool { return &b }

	tests := []struct {
		name          string
		tracked       *bool // last applied this process (nil = unknown)
		live          *bool // read from live cache (nil = no live cache)
		newEnableWiki bool
		wantChanged   bool
		explanation   string
	}{
		{
			name:          "unknown prior, no live cache: nothing to invalidate",
			tracked:       nil,
			live:          nil,
			newEnableWiki: true,
			wantChanged:   false,
			explanation:   "first build this process (e.g. startup) with no cache to compare",
		},
		{
			name:          "live cache authoritative: wiki on -> off changes",
			tracked:       nil,
			live:          bp(true),
			newEnableWiki: false,
			wantChanged:   true,
			explanation:   "startup-built cache had Wikipedia; user turns it off",
		},
		{
			name:          "live cache authoritative: unchanged",
			tracked:       bp(true), // stale tracked value must be ignored in favor of live
			live:          bp(false),
			newEnableWiki: false,
			wantChanged:   false,
			explanation:   "live cache (wiki off) matches the new setting",
		},
		{
			name:          "no live cache: fall back to tracked, change detected across disable",
			tracked:       bp(false), // last running cache was wiki-off
			live:          nil,       // feature was disabled in between -> DB rows survive
			newEnableWiki: true,      // re-enabling with wiki on
			wantChanged:   true,
			explanation:   "the gap this fix closes: re-enable after a disable still invalidates",
		},
		{
			name:          "no live cache: tracked matches new, no change",
			tracked:       bp(true),
			live:          nil,
			newEnableWiki: true,
			wantChanged:   false,
			explanation:   "re-enable with the same set that produced the surviving DB rows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := guideProviderSetChanged(tt.tracked, tt.live, tt.newEnableWiki)
			assert.Equalf(t, tt.wantChanged, got, "%s", tt.explanation)
		})
	}
}

// TestGuideWikipediaApplied_SequenceAcrossDisable walks the reconfigure state machine
// over a realistic sequence of saves, asserting at each step both whether the cache is
// invalidated and what provider set is remembered afterwards.
//
// It is the regression guard for the gap that guideProviderSetChanged alone cannot catch:
// that function was always correct in isolation, but its `tracked` input was never seeded,
// because the startup cache is built by initGuideCacheIfNeeded (api_service.go) rather than
// by the reconfigure handler. The final step is the bug — before nextGuideWikipediaApplied
// adopted the outgoing cache's set on a disable, both inputs were nil there, no invalidation
// ran, and Start() reloaded Wikipedia-authored rows after Wikipedia had been switched off.
func TestGuideWikipediaApplied_SequenceAcrossDisable(t *testing.T) {
	t.Parallel()

	bp := func(b bool) *bool { return &b }

	// Step 1 — process start: cache built outside the handler with Wikipedia ON.
	// Nothing is tracked yet; that is precisely the precondition for the bug.
	var tracked *bool
	require.Nil(t, tracked, "startup builds the cache outside the handler, so nothing is tracked")

	// Step 2 — user disables the species guide. EnableWikipedia is untouched (still true),
	// so no provider change; but a live cache IS observable and must be adopted.
	live := bp(true) // outgoing cache has Wikipedia registered
	assert.False(t, guideProviderSetChanged(tracked, live, true),
		"disabling the feature alone is not a provider-set change")
	tracked = nextGuideWikipediaApplied(tracked, live, true /*cfg*/, false /*cacheBuilt*/)
	require.NotNil(t, tracked, "the outgoing cache's provider set must be adopted on a disable")
	assert.True(t, *tracked, "adopted set is the one that wrote the surviving DB rows")

	// Step 3 — user re-enables the guide WITH Wikipedia turned off. No live cache exists
	// (the feature was off), so the decision rests entirely on the value adopted in step 2.
	live = nil
	assert.True(t, guideProviderSetChanged(tracked, live, false),
		"re-enabling without Wikipedia must invalidate the Wikipedia-authored rows")
	tracked = nextGuideWikipediaApplied(tracked, live, false /*cfg*/, true /*cacheBuilt*/)
	require.NotNil(t, tracked)
	assert.False(t, *tracked, "the newly built cache's set is now the tracked one")

	// Step 4 — an idempotent re-save must not invalidate again.
	live = bp(false)
	assert.False(t, guideProviderSetChanged(tracked, live, false),
		"re-saving with no effective change must not wipe a freshly populated cache")
}

// TestNextGuideWikipediaApplied covers the recording rule in isolation, including the
// no-op case where neither a cache nor a live value is observable.
func TestNextGuideWikipediaApplied(t *testing.T) {
	t.Parallel()

	bp := func(b bool) *bool { return &b }

	t.Run("cache built records the new setting", func(t *testing.T) {
		t.Parallel()
		got := nextGuideWikipediaApplied(bp(false), bp(false), true, true)
		require.NotNil(t, got)
		assert.True(t, *got, "a built cache owns the rows, so its own setting wins")
	})

	t.Run("no cache but live present adopts live", func(t *testing.T) {
		t.Parallel()
		// cfg says false, but the surviving rows came from the live (true) set.
		got := nextGuideWikipediaApplied(nil, bp(true), false, false)
		require.NotNil(t, got)
		assert.True(t, *got, "cfg says nothing about rows the outgoing cache wrote")
	})

	t.Run("neither keeps the tracked value", func(t *testing.T) {
		t.Parallel()
		got := nextGuideWikipediaApplied(bp(true), nil, false, false)
		require.NotNil(t, got)
		assert.True(t, *got, "nothing observable, so the prior value must persist")
	})

	t.Run("adopted value is a copy, not an alias of live", func(t *testing.T) {
		t.Parallel()
		live := bp(true)
		got := nextGuideWikipediaApplied(nil, live, false, false)
		require.NotNil(t, got)
		*live = false // mutating the source must not corrupt the recorded set
		assert.True(t, *got, "recorded set must not alias the caller's live value")
	})
}

// TestHandleReconfigureSpeciesGuide_MissingDepsIsSafe verifies the guard: with no
// API controller or metrics wired, the reconfigure handler logs and returns
// without panicking (rather than dereferencing a nil controller). The full swap
// orchestration is exercised by the QA hot-reload integration suite, since it
// requires a live *apiv2.Controller; its building blocks (initGuideCacheIfNeeded,
// guideProviderSetChanged, warmGuideCacheWithTopSpecies) are unit-tested here and
// in guide_cache_init_test.go.
func TestHandleReconfigureSpeciesGuide_MissingDepsIsSafe(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		(&ControlMonitor{}).handleReconfigureSpeciesGuide() // apiController + metrics are nil
	})
}
