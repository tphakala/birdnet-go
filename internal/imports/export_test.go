// Package imports export_test.go exposes internal helpers for use by tests in
// the imports_test package. This file is only compiled during testing.
package imports

import "time"

// TargetClipRelPathForTest exposes targetClipRelPath for black-box tests.
func TargetClipRelPathForTest(scientificName string, confidence float64, ts time.Time, srcExt string) string {
	return targetClipRelPath(scientificName, confidence, ts, srcExt)
}

// CheckDiskSpaceForTest exposes checkDiskSpace for black-box tests.
func CheckDiskSpaceForTest(exportPath string, requiredBytes uint64, freeSpaceFn func(string) (uint64, error)) error {
	return checkDiskSpace(exportPath, requiredBytes, freeSpaceFn)
}
