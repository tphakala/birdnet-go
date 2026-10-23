//go:build integration

package v2only

import "testing"

// TestV2OnlyDatastore_GetSpeciesDiversityData_RangeBoundaries_MySQL runs the range
// boundary cases on MySQL, whose date expression (DATE_ADD with integer DIV) must
// agree with the detected_at bounds the same way SQLite's date() does.
func TestV2OnlyDatastore_GetSpeciesDiversityData_RangeBoundaries_MySQL(t *testing.T) {
	assertDiversityRangeBoundaries(t, setupMySQLDatastore(t))
}
