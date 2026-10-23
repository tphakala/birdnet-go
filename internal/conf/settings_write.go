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

// UpdateSettings publishes a changed settings snapshot. It calls mutate with
// the snapshot that is current at that moment and a deep clone of it, then
// publishes the clone when mutate returns nil.
//
// Runtime writers of the global snapshot (the settings API, the model manager,
// the range filter, path correction) go through here, so one lock orders them
// and a writer cannot publish a snapshot built from a baseline that another
// writer has already replaced. The ruleguard rule SettingsPublishThroughWriter
// keeps new production code on this path.
//
// mutate may change updated freely and must treat current as read-only. When
// mutate returns an error, including ErrSettingsUnchanged, nothing is
// published and the error is returned as is. If no snapshot has been
// published, ErrSettingsNotLoaded is returned and mutate is not called.
//
// Rules for mutate, because the settings writer lock is held while it runs:
//   - Keep it pure: read its arguments and in-memory catalogs, and log, nothing
//     else. Do not call the classifier, the orchestrator, the model manager,
//     the control channel or the API's handleSettingsChanges from it; those
//     take locks (for example the model manager's mm.mu) whose holders call
//     UpdateSettings, which would deadlock.
//   - Do not call UpdateSettings, CompareAndStoreSettings or any function that
//     does (UpdateIncludedSpecies, ResetRangeFilterUpdateFlag,
//     ShouldUpdateRangeFilterToday) from it: the lock is not reentrant.
//   - Do not do disk I/O in it. SaveSettings has its own lock and is called
//     after UpdateSettings returns.
//
// Lock order: the writer lock is acquired after the classifier model manager's
// mm.mu (ModelManager.Uninstall holds mm.mu while its config writer calls
// UpdateSettings) and after the API controller's settingsMutex, and it is never
// held while acquiring either of them, so a caller that holds mm.mu or
// settingsMutex may call UpdateSettings. The save lock taken by SaveSettings is
// a leaf: nothing is acquired while holding it.
//
// The returned current and updated are the two snapshots passed to mutate;
// updated is the published one, immutable from now on. Both are nil on error.
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
