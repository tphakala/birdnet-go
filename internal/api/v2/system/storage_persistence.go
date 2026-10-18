package system

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/health/checks"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// Markers of an in-memory SQLite database path (see the data-dir rule in
// internal/analysis/database_service.go).
const (
	sqliteMemoryPath       = ":memory:"
	sqliteMemoryURIPrefix  = "file::memory:"
	sqliteMemoryModeMarker = "mode=memory"
)

// maxStoragePaths is the number of key paths storagePaths can return.
const maxStoragePaths = 4

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
	paths := make([]diagnostics.StoragePath, 0, maxStoragePaths)

	switch {
	case configFile != "":
		paths = append(paths, diagnostics.StoragePath{Label: diagnostics.StorageLabelConfig, Path: configFile, IsFile: true})
	case configDir != "":
		paths = append(paths, diagnostics.StoragePath{Label: diagnostics.StorageLabelConfig, Path: configDir})
	}

	if db := s.Output.SQLite.Path; !s.Output.MySQL.Enabled && db != "" && !isInMemorySQLite(db) {
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

// isInMemorySQLite reports whether a SQLite path names an in-memory database,
// which has no file to lose.
func isInMemorySQLite(p string) bool {
	return p == sqliteMemoryPath ||
		strings.HasPrefix(p, sqliteMemoryURIPrefix) ||
		strings.Contains(p, sqliteMemoryModeMarker)
}

// storagePersistenceReport evaluates the key paths against the mount table. The
// settings, the config location and the mount table are read on every call, so
// setting changes apply without a restart.
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
		return checks.StoragePersistenceReport{}
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
