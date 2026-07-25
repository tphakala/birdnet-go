package processor

// resetBuildClipPathFallbackOnce resets the keyed WARN guard so tests
// can exercise the fallback path multiple times. Test-only.
// Cleared rather than reassigned, for the reason spelled out on
// resetNativeSkipOnce below.
func resetBuildClipPathFallbackOnce() {
	clipPathExtFallbackLogged.seen.Clear()
	buildClipPathFallbackFired.Store(false)
}

// buildClipPathFallbackWarned returns true if the one-shot WARN has fired
// at least once since the last reset. Test-only.
func buildClipPathFallbackWarned() bool {
	return buildClipPathFallbackFired.Load()
}

// resetNativeSkipOnce re-arms the native-encoder fallback log guards so a test
// can observe the warning more than once per process. Without it the first test
// to hit an unsupported clip shape consumes the guard for the whole run, and a
// later assertion on that warning would fail for the wrong reason.
// The guards are cleared rather than reassigned. onceByKey holds a sync.Map,
// which is marked noCopy and must not be copied once used; Clear resets it in
// place, which is what these helpers actually want. Note that go vet does NOT
// catch the alternative: assigning a zero composite literal (guard = onceByKey{})
// copies no lock state, so copylocks stays silent and the mistake would be
// invisible. Clearing is a deliberate choice, not something a linter enforces.
func resetNativeSkipOnce() {
	nativeEncoderSkipLogged.seen.Clear()
}

// resetBatFormatDowngradeOnce re-arms the ultrasonic WAV-downgrade log guard for
// the same reason as resetNativeSkipOnce. Test-only.
func resetBatFormatDowngradeOnce() {
	batFormatDowngradeLogged.seen.Clear()
}

// resetStrandedFormatOnce re-arms the no-encoder-left WAV-fallback log guard.
// Test-only, same rationale as the two above.
func resetStrandedFormatOnce() {
	strandedFormatLogged.seen.Clear()
}
