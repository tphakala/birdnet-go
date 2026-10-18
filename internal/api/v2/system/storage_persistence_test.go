package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/health/checks"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

func labelsOf(paths []diagnostics.StoragePath) []string {
	labels := make([]string, len(paths))
	for i := range paths {
		labels[i] = paths[i].Label
	}
	return labels
}

func TestStoragePaths(t *testing.T) {
	t.Parallel()

	t.Run("fixed label order with the config file preferred", func(t *testing.T) {
		t.Parallel()
		paths := storagePaths(storageSettings(), "/config/config.yaml", "/config")
		assert.Equal(t, []string{
			diagnostics.StorageLabelConfig, diagnostics.StorageLabelDatabase,
			diagnostics.StorageLabelModels, diagnostics.StorageLabelClips,
		}, labelsOf(paths))
		assert.Equal(t, diagnostics.StoragePath{Label: diagnostics.StorageLabelConfig, Path: "/config/config.yaml"}, paths[0])
		assert.Equal(t, diagnostics.StoragePath{Label: diagnostics.StorageLabelDatabase, Path: "/data/birdnet.db", IsFile: true}, paths[1])
		assert.Equal(t, "/models", paths[2].Path)
		assert.False(t, paths[2].IsFile)
		assert.Equal(t, "/clips", paths[3].Path)
	})

	t.Run("config dir used without a config file", func(t *testing.T) {
		t.Parallel()
		paths := storagePaths(storageSettings(), "", "/config")
		assert.Equal(t, diagnostics.StoragePath{Label: diagnostics.StorageLabelConfig, Path: "/config"}, paths[0])
	})

	t.Run("no config location drops config", func(t *testing.T) {
		t.Parallel()
		paths := storagePaths(storageSettings(), "", "")
		assert.NotContains(t, labelsOf(paths), diagnostics.StorageLabelConfig)
	})

	t.Run("mysql drops database", func(t *testing.T) {
		t.Parallel()
		s := storageSettings()
		s.Output.MySQL.Enabled = true
		assert.NotContains(t, labelsOf(storagePaths(s, "", "/config")), diagnostics.StorageLabelDatabase)
	})

	for _, dbPath := range []string{":memory:", "file::memory:?cache=shared", "file:x?mode=memory"} {
		t.Run("database path "+dbPath+" is dropped", func(t *testing.T) {
			t.Parallel()
			s := storageSettings()
			s.Output.SQLite.Path = dbPath
			assert.NotContains(t, labelsOf(storagePaths(s, "", "/config")), diagnostics.StorageLabelDatabase)
		})
	}

	t.Run("export disabled drops clips", func(t *testing.T) {
		t.Parallel()
		s := storageSettings()
		s.Realtime.Audio.Export.Enabled = false
		assert.NotContains(t, labelsOf(storagePaths(s, "", "/config")), diagnostics.StorageLabelClips)
	})

	t.Run("blank export path is kept because it means the working directory", func(t *testing.T) {
		t.Parallel()
		s := storageSettings()
		s.Realtime.Audio.Export.Path = ""
		paths := storagePaths(s, "", "/config")
		assert.Contains(t, labelsOf(paths), diagnostics.StorageLabelClips)
	})
}

func TestStoragePaths_BlankSQLitePathJudgesDefault(t *testing.T) {
	t.Parallel()
	for _, blank := range []string{"", "   "} {
		s := storageSettings()
		s.Output.SQLite.Path = blank

		paths := storagePaths(s, "", "/config")

		var db *diagnostics.StoragePath
		for i := range paths {
			if paths[i].Label == diagnostics.StorageLabelDatabase {
				db = &paths[i]
			}
		}
		require.NotNil(t, db, "a blank path opens the default database, so it is judged: %q", blank)
		assert.Equal(t, "birdnet.db", db.Path, "the datastore default database file")
		assert.True(t, db.IsFile)
	}
}

func TestStoragePersistenceReport(t *testing.T) {
	t.Parallel()

	newHandler := func(env string, reader func() ([]diagnostics.MountTableEntry, error)) *Handler {
		f := newStorageFixture()
		f.setEnv(env)
		if reader != nil {
			f.h.storageDeps.mountTable = reader
		}
		return f.h
	}

	t.Run("outside Docker and Podman is not applicable and reads nothing", func(t *testing.T) {
		t.Parallel()
		h := newHandler(sysinfo.EnvLXC, func() ([]diagnostics.MountTableEntry, error) {
			require.Fail(t, "mount table must not be read outside Docker and Podman")
			return nil, nil
		})
		report := h.storagePersistenceReport()
		assert.False(t, report.Applicable)
		assert.Empty(t, report.Verdicts)
	})

	t.Run("a reader error is reported", func(t *testing.T) {
		t.Parallel()
		boom := errors.NewStd("boom")
		h := newHandler(sysinfo.EnvDocker, func() ([]diagnostics.MountTableEntry, error) { return nil, boom })
		report := h.storagePersistenceReport()
		assert.True(t, report.Applicable)
		require.ErrorIs(t, report.Err, boom)
	})

	t.Run("verdicts follow the mount table", func(t *testing.T) {
		t.Parallel()
		h := newHandler(sysinfo.EnvPodman, func() ([]diagnostics.MountTableEntry, error) {
			return []diagnostics.MountTableEntry{
				{Destination: "/", Root: "/", FSType: "overlay"},
				{Destination: "/config", Root: "/h/config", FSType: "ext4"},
			}, nil
		})
		report := h.storagePersistenceReport()
		require.NoError(t, report.Err)
		require.True(t, report.Applicable)
		require.Len(t, report.Verdicts, 4)
		assert.Equal(t, diagnostics.PersistencePersistent, report.Verdicts[0].Class)
		assert.Equal(t, diagnostics.PersistenceContainerLayer, report.Verdicts[1].Class)
	})

	t.Run("config file bind mounted alone is persistent, database file beside overlay is not", func(t *testing.T) {
		t.Parallel()
		h := newHandler(sysinfo.EnvDocker, func() ([]diagnostics.MountTableEntry, error) {
			return []diagnostics.MountTableEntry{
				{Destination: "/", Root: "/", FSType: "overlay"},
				{Destination: "/config/config.yaml", Root: "/h/config.yaml", FSType: "ext4"},
				{Destination: "/data/birdnet.db", Root: "/h/birdnet.db", FSType: "ext4"},
			}, nil
		})
		report := h.storagePersistenceReport()
		require.NoError(t, report.Err)
		require.Len(t, report.Verdicts, 4)
		assert.Equal(t, diagnostics.StorageLabelConfig, report.Verdicts[0].Label)
		assert.Equal(t, diagnostics.PersistencePersistent, report.Verdicts[0].Class, "the config file has its own mount")
		assert.Equal(t, diagnostics.StorageLabelDatabase, report.Verdicts[1].Label)
		assert.Equal(t, diagnostics.PersistenceContainerLayer, report.Verdicts[1].Class, "SQLite sidecar files sit beside the database in the directory")
	})
}

func TestStoragePersistenceReport_NilSettingsKeepsState(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 0)
	f := newNoticeFixture(rec)
	f.set("models", classLayer)
	f.sync()
	f.h.storageDeps.settings = func() *conf.Settings { return nil }

	report := f.h.storagePersistenceReport()

	assert.True(t, report.Applicable, "a missing settings snapshot does not make the check not applicable")
	require.Error(t, report.Err)
	f.sync()
	created, deleted := rec.counts()
	assert.Equal(t, 1, created)
	assert.Zero(t, deleted, "the live notice is kept while the settings are unavailable")
	res := checks.NewStoragePersistenceCheck(f.h.storagePersistenceReport).Run(t.Context())
	assert.Equal(t, health.StatusUnknown, res.Status)
	assert.Equal(t, "Storage state could not be read", res.Message)
}
