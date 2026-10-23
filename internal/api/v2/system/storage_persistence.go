package system

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health/checks"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// errStorageSettingsUnavailable is the report error while no settings snapshot
// exists yet; the notices keep their state and the health check is unknown.
var errStorageSettingsUnavailable = errors.NewStd("settings snapshot unavailable")

// storageDeps are the environment reads behind the storage persistence report.
// A nil field means the real implementation; tests replace them so they never
// touch the runner's mount table, home directory or settings.
type storageDeps struct {
	// envType returns the sysinfo environment type.
	envType func() string
	// settings returns the current settings snapshot.
	settings func() *conf.Settings
	// configLocation returns the config file, or when none exists the config
	// directory.
	configLocation func() (file, dir string)
	// mountTable reads the mount table of this process.
	mountTable func() ([]diagnostics.MountTableEntry, error)
	// resolve resolves a configured path to the real path.
	resolve func(string) (string, error)
}

// storagePaths lists the key paths to judge, in a fixed label order. It is pure:
// the config location is passed in. The paths are the ones the application
// writes to, taken verbatim from the settings, so the verdict describes the
// storage the application really uses.
func storagePaths(s *conf.Settings, configFile, configDir string) []diagnostics.StoragePath {
	var paths []diagnostics.StoragePath

	// The config file is judged as itself, so a file bind mounted on its own is
	// found; it has no sidecar files. The database is judged by its directory
	// because SQLite writes its -wal, -shm and -journal files beside it.
	switch {
	case configFile != "":
		paths = append(paths, diagnostics.StoragePath{Label: diagnostics.StorageLabelConfig, Path: configFile})
	case configDir != "":
		paths = append(paths, diagnostics.StoragePath{Label: diagnostics.StorageLabelConfig, Path: configDir})
	}

	// A blank SQLite path opens the datastore's default database, so it is judged.
	if db := datastore.ResolveSQLitePath(s.Output.SQLite.Path); !s.Output.MySQL.Enabled && !isInMemorySQLite(db) {
		paths = append(paths, diagnostics.StoragePath{Label: diagnostics.StorageLabelDatabase, Path: db, IsFile: true})
	}

	if dir, ok := s.ResolveModelsDir(); ok {
		paths = append(paths, diagnostics.StoragePath{Label: diagnostics.StorageLabelModels, Path: dir})
	}

	// A blank clip path is kept: clips are written under filepath.Join(path,
	// name), so a blank path means the working directory.
	if s.Realtime.Audio.Export.Enabled {
		paths = append(paths, diagnostics.StoragePath{Label: diagnostics.StorageLabelClips, Path: s.Realtime.Audio.Export.Path})
	}
	return paths
}

// Markers of an in-memory SQLite database path.
const (
	sqliteMemoryPath       = ":memory:"
	sqliteMemoryURIPrefix  = "file::memory:"
	sqliteMemoryModeMarker = "mode=memory"
)

// isInMemorySQLite reports whether a SQLite path names an in-memory database,
// which has no file to lose (see the data-dir rule in
// internal/analysis/database_service.go).
func isInMemorySQLite(p string) bool {
	return p == sqliteMemoryPath || strings.HasPrefix(p, sqliteMemoryURIPrefix) || strings.Contains(p, sqliteMemoryModeMarker)
}

// storagePersistenceReport evaluates the key paths against the mount table. The
// settings, the config location and the mount table are read on every call, so
// setting changes apply without a restart. Outside Docker and Podman the report
// is not applicable; a missing settings snapshot or an unreadable mount table
// gives an applicable report carrying an error.
func (c *Handler) storagePersistenceReport() checks.StoragePersistenceReport {
	d := c.storageDeps
	envType := func() string {
		env, _ := sysinfo.GetEnvironment()
		return env
	}
	if d.envType != nil {
		envType = d.envType
	}
	if !diagnostics.StoragePersistenceAppliesTo(envType()) {
		return checks.StoragePersistenceReport{}
	}

	settings := c.CurrentSettings
	if d.settings != nil {
		settings = d.settings
	}
	s := settings()
	if s == nil {
		return checks.StoragePersistenceReport{Applicable: true, Err: errStorageSettingsUnavailable}
	}

	location := defaultConfigLocation
	if d.configLocation != nil {
		location = d.configLocation
	}
	configFile, configDir := location()

	readTable := diagnostics.ReadOwnMountTable
	if d.mountTable != nil {
		readTable = d.mountTable
	}
	table, err := readTable()
	if err != nil {
		return checks.StoragePersistenceReport{Applicable: true, Err: err}
	}

	resolve := diagnostics.ResolveRealPath
	if d.resolve != nil {
		resolve = d.resolve
	}
	return checks.StoragePersistenceReport{
		Applicable: true,
		Verdicts:   diagnostics.EvaluateStoragePersistence(table, storagePaths(s, configFile, configDir), resolve),
	}
}

// defaultConfigLocation returns the active config file, or when none exists yet
// the directory it would be created in.
func defaultConfigLocation() (file, dir string) {
	if f, err := conf.FindConfigFile(); err == nil {
		return f, ""
	}
	if d, err := conf.ResolveConfigDir(); err == nil {
		return "", d
	}
	return "", ""
}
