package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
