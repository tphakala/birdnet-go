package apicore

import "fmt"

// FormatBytesUint64 formats bytes into human-readable format (for uint64 values).
// It is shared substrate: the system domain's database-stats/backup handlers and
// the package-api async backup handlers both render byte counts with it.
func FormatBytesUint64(bytes uint64) string {
	const unit uint64 = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := unit, 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
