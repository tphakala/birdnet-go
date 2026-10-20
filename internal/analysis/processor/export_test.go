package processor

// resetBuildClipPathFallbackOnce resets the keyed WARN guard so tests
// can exercise the fallback path multiple times. Test-only.
// Cleared rather than reassigned: onceByKey holds a sync.Map, which is marked
// noCopy and must not be copied once used; Clear resets it in place. Note that go
// vet does NOT catch assigning a zero composite literal (guard = onceByKey{}), so
// clearing is a deliberate choice, not something a linter enforces.
func resetBuildClipPathFallbackOnce() {
	clipPathExtFallbackLogged.seen.Clear()
	buildClipPathFallbackFired.Store(false)
}

// buildClipPathFallbackWarned returns true if the one-shot WARN has fired
// at least once since the last reset. Test-only.
func buildClipPathFallbackWarned() bool {
	return buildClipPathFallbackFired.Load()
}
