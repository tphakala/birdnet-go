//go:build !unix

package discovery

import "os"

// classifyOpenError maps a birds.db open failure to a candidate Reason. gorm and
// the SQLite driver return the same opaque "unable to open database file" for both
// an unreadable file and a corrupt or non-SQLite one, so this re-tests readability
// directly to tell a permission problem apart from a bad file.
//
// On non-unix systems (Windows) FIFOs that can block on open are not a concern,
// so a plain os.Open is used.
func classifyOpenError(dbPath string) string {
	f, err := os.Open(dbPath)
	if err != nil {
		if os.IsPermission(err) {
			return ReasonPermissionDenied
		}
		return ReasonOpenFailed
	}
	_ = f.Close()
	return ReasonOpenFailed
}
