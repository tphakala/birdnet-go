// Package speciestest provides test-support helpers for the species package.
// These helpers live in a dedicated importable subpackage (rather than a
// _test.go file) so they can be shared across other packages' tests, and they
// are kept out of the production build because production code never imports
// this package.
package speciestest

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/species"
)

// testOnlyHelperPanic is the panic message for helpers called outside tests.
const testOnlyHelperPanic = "species: test-only helper called outside of test execution"

// panicIfNotTesting is the tripwire shared by every helper in this package.
func panicIfNotTesting() {
	if !testing.Testing() {
		panic(testOnlyHelperPanic)
	}
}

// SetCurrentYearForTesting sets the tracker's current year to a deterministic
// value for tests. It panics if called outside of test execution.
func SetCurrentYearForTesting(tb testing.TB, t *species.SpeciesTracker, year int) {
	tb.Helper()
	panicIfNotTesting()
	t.SetCurrentYearOverride(year)
}

// InitFromDatabaseAt loads the tracker from its database as if the load ran at
// now, so a test gets its fixture year regardless of the real clock. It panics
// if called outside of test execution. It records now as the sync time, so do
// not call SyncIfNeeded afterwards in the same test: it would reload at the
// real clock.
func InitFromDatabaseAt(tb testing.TB, t *species.SpeciesTracker, now time.Time) error {
	tb.Helper()
	panicIfNotTesting()
	return t.InitFromDatabaseAt(now)
}
