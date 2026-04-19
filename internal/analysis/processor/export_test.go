package processor

import "sync"

// resetBuildClipPathFallbackOnce resets the one-shot WARN guard so tests
// can exercise the fallback path multiple times. Test-only.
func resetBuildClipPathFallbackOnce() {
	buildClipPathFallbackOnce = sync.Once{}
	buildClipPathFallbackFired.Store(false)
}

// buildClipPathFallbackWarned returns true if the one-shot WARN has fired
// at least once since the last reset. Test-only.
func buildClipPathFallbackWarned() bool {
	return buildClipPathFallbackFired.Load()
}
