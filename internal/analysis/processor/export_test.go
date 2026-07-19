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

// resetNativeSkipOnce re-arms the native-encoder fallback log guards so a test
// can observe the warning more than once per process. Without it the first test
// to hit an unsupported clip shape consumes the Once for the whole run, and a
// later assertion on that warning would fail for the wrong reason.
func resetNativeSkipOnce() {
	nativeAACSkipOnce = sync.Once{}
	nativeOpusSkipOnce = sync.Once{}
}
