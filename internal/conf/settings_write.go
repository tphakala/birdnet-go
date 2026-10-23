package conf

import (
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// settingsWriteMu serializes every clone-mutate-publish cycle on the global
// settings snapshot, so a writer cannot publish a snapshot built from a
// baseline that another writer has since replaced. See UpdateSettings for the
// lock order and the rules for code that runs under it.
var settingsWriteMu sync.Mutex

// ErrSettingsUnchanged is returned by an UpdateSettings mutate function to
// say that, after looking at the current snapshot, there is nothing to publish.
// UpdateSettings publishes nothing and returns it unchanged; callers that treat
// "nothing to do" as success test for it with errors.Is.
var ErrSettingsUnchanged = errors.NewStd("settings unchanged")

// ErrSettingsNotLoaded is returned by UpdateSettings when no settings snapshot
// has been published yet, so there is no baseline to build an update from.
var ErrSettingsNotLoaded = errors.NewStd("settings not loaded")

// UpdateSettings publishes a changed settings snapshot: it calls mutate with the
// snapshot that is current under the settings writer lock and a deep clone of it,
// and publishes the clone when mutate returns nil. Runtime writers go through
// here (the ruleguard rule SettingsPublishThroughWriter enforces it), so a writer
// cannot publish over a change another writer made after it read its baseline.
//
// mutate may change updated freely and must treat current as read-only. If
// mutate returns an error, including ErrSettingsUnchanged, nothing is published
// and nil snapshots are returned with the error. If no snapshot is published,
// ErrSettingsNotLoaded is returned and mutate is not called.
//
// The lock is held while mutate runs, so mutate must:
//   - take no other lock and not call the classifier, the orchestrator, the model
//     manager, the control channel or the API's handleSettingsChanges, whose locks
//     (for example mm.mu) are held by callers of UpdateSettings, which would deadlock;
//   - not call UpdateSettings, CompareAndStoreSettings or a function that does
//     (UpdateIncludedSpecies, ResetRangeFilterUpdateFlag,
//     ShouldUpdateRangeFilterToday): the lock is not reentrant;
//   - not write to disk or wait on unbounded I/O; settings validation is the one
//     exception, and its ffmpeg probe has a timeout. Call SaveSettings after
//     UpdateSettings returns.
//
// Lock order: the writer lock is acquired after the model manager's mm.mu
// (ModelManager.Uninstall holds mm.mu while its config writer calls
// UpdateSettings) and the API controller's settingsMutex, and is never held while
// acquiring either. The save lock taken by SaveSettings is a leaf.
func UpdateSettings(mutate func(current, updated *Settings) error) (current, updated *Settings, err error) {
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()

	current = settingsInstance.Load()
	if current == nil {
		return nil, nil, ErrSettingsNotLoaded
	}
	updated = CloneSettings(current)
	if err := mutate(current, updated); err != nil {
		return nil, nil, err
	}
	settingsInstance.Store(updated)
	return current, updated, nil
}

// CompareAndStoreSettings publishes next only if old is still the published
// snapshot, and reports whether it did. It is how a writer that published old
// earlier takes its own change back without removing a snapshot another writer
// published on top of it: when it returns false the newer snapshot is left in
// place and the caller must say so rather than claim a rollback.
//
// It serializes with UpdateSettings, so it can be called from any context that
// UpdateSettings can, and it must not be called from inside a mutate function.
// Passing a nil old matches only an unloaded state and is not useful.
func CompareAndStoreSettings(old, next *Settings) bool {
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()
	return settingsInstance.CompareAndSwap(old, next)
}
