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

// SumSourceClipSizesForTest exposes sumSourceClipSizes for black-box tests.
func SumSourceClipSizesForTest(audioSourceDir string, rows []SourceDetection) uint64 {
	return sumSourceClipSizes(audioSourceDir, rows)
}

// SetSumClipSizesForTest replaces the engine's clip-size estimator and returns
// a restore function. It lets black-box tests observe the disk-space estimate's
// input (the candidate rows) without depending on free disk space.
func SetSumClipSizesForTest(e *Engine, f func(audioSourceDir string, rows []SourceDetection) uint64) func() {
	orig := e.sumClipSizes
	e.sumClipSizes = f
	return func() { e.sumClipSizes = orig }
}
