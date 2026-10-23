package system

import (
	"path"
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// Config location the storage tests use for every handler.
const (
	storageTestConfigFile = "/config/config.yaml"
	storageTestConfigDir  = "/config"
)

// storageSettings returns settings with SQLite, the models directory and clip
// export configured.
func storageSettings() *conf.Settings {
	s := &conf.Settings{}
	s.Output.SQLite.Path = "/data/birdnet.db"
	s.Models.Directory = "/models"
	s.Realtime.Audio.Export.Enabled = true
	s.Realtime.Audio.Export.Path = "/clips"
	return s
}

// storageFixture is a Handler whose storage dependencies describe a container
// where each label has a chosen persistence class, so tests exercise the real
// storagePersistenceReport.
type storageFixture struct {
	h *Handler

	mu       sync.Mutex
	settings *conf.Settings
	classes  map[string]diagnostics.PersistenceClass
	env      string
	readErr  error

	// reports counts storage evaluations that reached the environment check.
	reports atomic.Int32
}

// newStorageFixture returns a fixture in a Docker container with every label
// on a persistent mount.
func newStorageFixture() *storageFixture {
	f := &storageFixture{
		settings: storageSettings(),
		env:      sysinfo.EnvDocker,
		classes: map[string]diagnostics.PersistenceClass{
			diagnostics.StorageLabelConfig:   diagnostics.PersistencePersistent,
			diagnostics.StorageLabelDatabase: diagnostics.PersistencePersistent,
			diagnostics.StorageLabelModels:   diagnostics.PersistencePersistent,
			diagnostics.StorageLabelClips:    diagnostics.PersistencePersistent,
		},
	}
	f.h = &Handler{Core: &apicore.Core{}, storageDeps: storageDeps{
		envType: func() string {
			f.reports.Add(1)
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.env
		},
		settings:       func() *conf.Settings { f.mu.Lock(); defer f.mu.Unlock(); return f.settings },
		configLocation: func() (string, string) { return storageTestConfigFile, storageTestConfigDir },
		mountTable:     f.mountTable,
		resolve:        f.resolve,
	}}
	return f
}

// set changes the class of a label.
func (f *storageFixture) set(label string, class diagnostics.PersistenceClass) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.classes[label] = class
}

// update runs fn on the settings under the fixture lock.
func (f *storageFixture) update(fn func(*conf.Settings)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.settings)
}

// setEnv changes the environment type.
func (f *storageFixture) setEnv(env string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.env = env
}

// setReadErr makes the mount table unreadable (nil makes it readable again).
func (f *storageFixture) setReadErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readErr = err
}

// judgedDir is the location of a path that the mount table describes: the
// directory of a file judged by its directory, else the path itself.
func judgedDir(sp diagnostics.StoragePath) string {
	if sp.IsFile {
		return path.Dir(sp.Path)
	}
	return sp.Path
}

// mountTable builds a table with the overlay root and one mount per label whose
// class needs one: no mount for the container layer, a tmpfs for memory, an
// anonymous volume root, or a bind mount otherwise.
func (f *storageFixture) mountTable() ([]diagnostics.MountTableEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	table := []diagnostics.MountTableEntry{{Destination: "/", Root: "/", FSType: "overlay"}}
	for _, sp := range storagePaths(f.settings, storageTestConfigFile, storageTestConfigDir) {
		dest := judgedDir(sp)
		switch f.classes[sp.Label] {
		case diagnostics.PersistenceMemory:
			table = append(table, diagnostics.MountTableEntry{Destination: dest, Root: "/", FSType: "tmpfs"})
		case diagnostics.PersistenceAnonymousVolume:
			table = append(table, diagnostics.MountTableEntry{
				Destination: dest,
				Root:        "/var/lib/docker/volumes/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/_data",
				FSType:      "ext4",
			})
		case diagnostics.PersistenceContainerLayer:
		case diagnostics.PersistencePersistent, diagnostics.PersistenceUnknown:
			table = append(table, diagnostics.MountTableEntry{Destination: dest, Root: "/host" + dest, FSType: "ext4"})
		}
	}
	return table, nil
}

// resolve returns the path itself, or an error for the path of a label whose
// class is unknown.
func (f *storageFixture) resolve(p string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, sp := range storagePaths(f.settings, storageTestConfigFile, storageTestConfigDir) {
		if sp.Path == p && f.classes[sp.Label] == diagnostics.PersistenceUnknown {
			return "", errors.NewStd("cannot resolve " + p)
		}
	}
	return p, nil
}
