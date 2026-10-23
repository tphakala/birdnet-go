//go:build integration

package v2only

import "testing"

// TestV2OnlyDatastore_GetSpeciesLastDetectionDateBefore_LatestAcrossLabels_MySQL runs
// the per-label latest-detection cases on MySQL, where the correlated ORDER BY ...
// LIMIT 1 scalar subquery and the Go-side date bucketing must agree with SQLite.
func TestV2OnlyDatastore_GetSpeciesLastDetectionDateBefore_LatestAcrossLabels_MySQL(t *testing.T) {
	assertLastDetectionDateAcrossLabels(t, setupMySQLDatastore(t))
}
