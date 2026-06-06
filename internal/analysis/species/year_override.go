package species

// SetCurrentYearOverride overrides the internal tracking year, bypassing the
// normal year derivation logic. It is intended for integration tests that need
// a deterministic year.
//
// WARNING: this method is guard-free (it has no testing.Testing() tripwire,
// unlike the test-only methods in testing_test.go) because it must be reachable
// from cross-package test-support code (speciestest). Do NOT call it from
// production code: it manipulates the internal currentYear field directly and
// misuse leads to inconsistent tracking data.
func (t *SpeciesTracker) SetCurrentYearOverride(year int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.currentYear = year
}
