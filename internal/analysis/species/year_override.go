package species

// SetCurrentYearOverride overrides the internal tracking year, bypassing the
// normal year derivation logic. It is intended for integration tests that need
// a deterministic year. The year is a tracking year: the year whose reset date
// opens the current yearly period, which is the calendar year for the default
// January 1 reset.
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
