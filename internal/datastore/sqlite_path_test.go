package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveSQLitePath verifies that a blank configured path falls back to the
// default while any non-blank path (including SQLite special forms) is returned
// unchanged. A blank path previously flowed into buildSQLiteDSN and produced a
// bare "?<pragmas>" DSN, causing the driver to create a file literally named
// after the pragma query string.
func TestResolveSQLitePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		expected   string
	}{
		{name: "empty string falls back to default", configured: "", expected: defaultSQLitePath},
		{name: "whitespace-only falls back to default", configured: "   ", expected: defaultSQLitePath},
		{name: "tab and newline fall back to default", configured: "\t\n", expected: defaultSQLitePath},
		{name: "plain relative path preserved", configured: "birdnet.db", expected: "birdnet.db"},
		{name: "absolute path preserved", configured: "/data/birdnet.db", expected: "/data/birdnet.db"},
		{name: "in-memory path preserved", configured: ":memory:", expected: ":memory:"},
		{name: "shared in-memory path preserved", configured: "file::memory:?cache=shared", expected: "file::memory:?cache=shared"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, resolveSQLitePath(tc.configured))
		})
	}
}

// TestResolveSQLitePathNeverProducesBareDSN guards the specific failure mode:
// feeding the resolved path into buildSQLiteDSN must never yield a DSN that
// begins with "?", which the SQLite driver would open as a bogus filename.
func TestResolveSQLitePathNeverProducesBareDSN(t *testing.T) {
	t.Parallel()

	const pragmas = "_journal_mode=WAL&_busy_timeout=30000"
	for _, configured := range []string{"", "   ", "\t"} {
		dsn := buildSQLiteDSN(resolveSQLitePath(configured), pragmas)
		assert.NotEmpty(t, dsn)
		assert.NotEqual(t, byte('?'), dsn[0], "DSN must not start with '?' for input %q", configured)
	}
}
