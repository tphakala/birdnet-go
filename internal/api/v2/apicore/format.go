package apicore

import "github.com/tphakala/birdnet-go/internal/formatutil"

// FormatBytesUint64 formats bytes into human-readable format (for uint64 values).
// It is shared substrate: the system domain's database-stats/backup handlers and
// the package-api async backup handlers both render byte counts with it. The
// formatting itself lives in the leaf formatutil package so this and the other
// former copies stay in lockstep; this wrapper preserves the exported API its
// external callers depend on.
func FormatBytesUint64(bytes uint64) string {
	return formatutil.BytesUint64(bytes)
}
