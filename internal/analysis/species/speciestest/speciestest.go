// Package speciestest provides test-support helpers for the species package.
// These helpers live in a dedicated importable subpackage (rather than a
// _test.go file) so they can be shared across other packages' tests, and they
// are kept out of the production build because production code never imports
// this package.
package speciestest

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/analysis/species"
)

// SetCurrentYearForTesting sets the tracker's current year to a deterministic
// value for tests. It panics if called outside of test execution.
func SetCurrentYearForTesting(tb testing.TB, t *species.SpeciesTracker, year int) {
	tb.Helper()
	if !testing.Testing() {
		panic("species: test-only helper called outside of test execution")
	}
	t.SetCurrentYearOverride(year)
}
