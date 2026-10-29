package checks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestDatabaseSizeCheck_Run(t *testing.T) {
	t.Parallel()

	content := []byte("sqlite file content")
	dbFile := filepath.Join(t.TempDir(), "birdnet.db")
	require.NoError(t, os.WriteFile(dbFile, content, 0o600))
	const serverSizeBytes int64 = 5 << 20

	probe := func(size int64, applicable bool, err error) DatabaseSizeProbe {
		return func(context.Context) (int64, bool, error) { return size, applicable, err }
	}

	tests := []struct {
		name        string
		probe       DatabaseSizeProbe
		wantStatus  health.Status
		wantMessage string
		wantSize    int64
		wantPath    bool
	}{
		{"mysql schema size", probe(serverSizeBytes, true, nil), health.StatusHealthy, "5.0 MB", serverSizeBytes, false},
		{"mysql schema above the sqlite warning size", probe(warnSizeBytes+1, true, nil), health.StatusHealthy, "Database size OK", warnSizeBytes + 1, false},
		{"probe error", probe(0, true, errors.NewStd("connection refused")), health.StatusUnknown, "Unable to read the database size: connection refused", 0, false},
		{"probe error while not applicable", probe(0, false, errors.NewStd("connection refused")), health.StatusUnknown, "Unable to read the database size", 0, false},
		{"not mysql uses the sqlite file", probe(0, false, nil), health.StatusHealthy, "Database size OK", int64(len(content)), true},
		{"nil probe uses the sqlite file", nil, health.StatusHealthy, "Database size OK", int64(len(content)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			check := NewDatabaseSizeCheck(func() string { return dbFile }, tt.probe)
			result := check.Run(t.Context())
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Contains(t, result.Message, tt.wantMessage)
			if tt.wantStatus == health.StatusUnknown {
				assert.NotContains(t, result.Details, "path")
				return
			}
			assert.Equal(t, tt.wantSize, result.Details["size_bytes"])
			if tt.wantPath {
				assert.Equal(t, dbFile, result.Details["path"])
			} else {
				assert.NotContains(t, result.Details, "path")
			}
		})
	}
}

func TestDatabaseIntegrityCheck_Healthy(t *testing.T) {
	check := NewDatabaseIntegrityCheck(func() (string, bool) {
		return "ok", false
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Equal(t, "database_integrity", result.Name)
	assert.Equal(t, health.CategoryDatabase, result.Category)
	assert.Contains(t, result.Message, "passed")
}

func TestDatabaseIntegrityCheck_Corrupted(t *testing.T) {
	check := NewDatabaseIntegrityCheck(func() (string, bool) {
		return "database disk image is malformed", true
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
	assert.Contains(t, result.Message, "corruption")
	recoveryHint, ok := result.Details["recovery_hint"].(string)
	assert.True(t, ok, "recovery_hint should be a string")
	assert.Contains(t, recoveryHint, "Support")
}

func TestDatabaseIntegrityCheck_NotYetRun(t *testing.T) {
	check := NewDatabaseIntegrityCheck(func() (string, bool) {
		return "", false
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusUnknown, result.Status)
	assert.Contains(t, result.Message, "not run yet")
}

func TestDatabaseIntegrityCheck_NilProvider(t *testing.T) {
	check := NewDatabaseIntegrityCheck(nil)

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestDatabaseIntegrityCheck_ResultNotOkButNotLatched(t *testing.T) {
	check := NewDatabaseIntegrityCheck(func() (string, bool) {
		return "*** in database main ***\nPage 42: btreeInitPage() error", false
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status,
		"non-ok integrity result should be critical even without latch")
}
