package checks

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
)

// storageReport builds an applicable report from label and class pairs.
func storageReport(pairs ...string) StoragePersistenceReport {
	r := StoragePersistenceReport{Applicable: true}
	for i := 0; i+1 < len(pairs); i += 2 {
		r.Verdicts = append(r.Verdicts, diagnostics.StorageVerdict{
			Label: pairs[i],
			Path:  "/p/" + pairs[i],
			Class: diagnostics.PersistenceClass(pairs[i+1]),
		})
	}
	return r
}

func runStorageCheck(t *testing.T, r StoragePersistenceReport) health.Result {
	t.Helper()
	return NewStoragePersistenceCheck(func() StoragePersistenceReport { return r }).Run(t.Context())
}

func TestStoragePersistenceCheck_NameAndCategory(t *testing.T) {
	t.Parallel()
	c := NewStoragePersistenceCheck(nil)
	assert.Equal(t, "storage_persistence", c.Name())
	assert.Equal(t, health.CategoryConfig, c.Category())
}

func TestStoragePersistenceCheck_SkippedAndUnknown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		check   *StoragePersistenceCheck
		status  health.Status
		message string
	}{
		{
			name:    "nil provider",
			check:   NewStoragePersistenceCheck(nil),
			status:  health.StatusSkipped,
			message: msgStorageNotContainer,
		},
		{
			name: "not applicable",
			check: NewStoragePersistenceCheck(func() StoragePersistenceReport {
				return StoragePersistenceReport{}
			}),
			status:  health.StatusSkipped,
			message: msgStorageNotContainer,
		},
		{
			name: "no paths",
			check: NewStoragePersistenceCheck(func() StoragePersistenceReport {
				return StoragePersistenceReport{Applicable: true}
			}),
			status:  health.StatusSkipped,
			message: msgStorageNoPaths,
		},
		{
			name: "read error",
			check: NewStoragePersistenceCheck(func() StoragePersistenceReport {
				return StoragePersistenceReport{Applicable: true, Err: errors.NewStd("denied")}
			}),
			status: health.StatusUnknown,
		},
		{
			name: "all unknown",
			check: NewStoragePersistenceCheck(func() StoragePersistenceReport {
				return storageReport(diagnostics.StorageLabelConfig, string(diagnostics.PersistenceUnknown))
			}),
			status: health.StatusUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := tt.check.Run(t.Context())
			assert.Equal(t, tt.status, res.Status)
			assert.Equal(t, "storage_persistence", res.Name)
			assert.Equal(t, health.CategoryConfig, res.Category)
			if tt.message != "" {
				assert.Equal(t, tt.message, res.Message)
			}
		})
	}
}

func TestStoragePersistenceCheck_SeverityMapping(t *testing.T) {
	t.Parallel()
	const (
		persistent = string(diagnostics.PersistencePersistent)
		layer      = string(diagnostics.PersistenceContainerLayer)
		anon       = string(diagnostics.PersistenceAnonymousVolume)
		unknown    = string(diagnostics.PersistenceUnknown)
	)
	tests := []struct {
		name    string
		report  StoragePersistenceReport
		status  health.Status
		message string
	}{
		{
			name: "all persistent",
			report: storageReport(
				diagnostics.StorageLabelConfig, persistent,
				diagnostics.StorageLabelModels, persistent),
			status:  health.StatusHealthy,
			message: msgStorageAllPersistent,
		},
		{
			name: "persistent with an unknown path",
			report: storageReport(
				diagnostics.StorageLabelConfig, persistent,
				diagnostics.StorageLabelModels, unknown),
			status:  health.StatusHealthy,
			message: "No data path is lost, but some paths could not be judged",
		},
		{
			name: "models on the container layer",
			report: storageReport(
				diagnostics.StorageLabelConfig, persistent,
				diagnostics.StorageLabelModels, layer),
			status:  health.StatusWarning,
			message: "Lost when the container is recreated: models",
		},
		{
			name: "database on the container layer",
			report: storageReport(
				diagnostics.StorageLabelDatabase, layer,
				diagnostics.StorageLabelModels, layer),
			status:  health.StatusCritical,
			message: "Lost when the container is recreated: database, models",
		},
		{
			name:    "anonymous volume only",
			report:  storageReport(diagnostics.StorageLabelConfig, anon),
			status:  health.StatusWarning,
			message: "In an anonymous volume (can seem lost after a fresh container start): config",
		},
		{
			name: "both kinds",
			report: storageReport(
				diagnostics.StorageLabelConfig, anon,
				diagnostics.StorageLabelClips, layer),
			status:  health.StatusWarning,
			message: "Lost when the container is recreated: clips; In an anonymous volume (can seem lost after a fresh container start): config",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := runStorageCheck(t, tt.report)
			assert.Equal(t, tt.status, res.Status)
			assert.Equal(t, tt.message, res.Message)
		})
	}
}

func TestStoragePersistenceCheck_NoHostPaths(t *testing.T) {
	t.Parallel()
	table := []diagnostics.MountTableEntry{
		{Destination: "/", Root: "/", FSType: "overlay"},
		{Destination: "/config", Root: "/home/alice/birdnet/config", FSType: "ext4"},
	}
	verdicts := diagnostics.EvaluateStoragePersistence(table,
		[]diagnostics.StoragePath{{Label: diagnostics.StorageLabelConfig, Path: "/config"}},
		func(p string) (string, error) { return p, nil })
	require.Len(t, verdicts, 1)

	res := runStorageCheck(t, StoragePersistenceReport{Applicable: true, Verdicts: verdicts})

	raw, err := json.Marshal(res)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "alice")
	paths, ok := res.Details["paths"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, paths, 1)
	assert.Equal(t, "/config", paths[0]["mount_point"])
	assert.Equal(t, "ext4", paths[0]["fstype"])
	assert.Equal(t, "persistent", paths[0]["class"])
}
