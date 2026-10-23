package conf

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// settingsWriteMu serializes every clone-mutate-publish cycle on the global
// settings snapshot, so a writer cannot publish a snapshot built from a
// baseline that another writer has since replaced. See UpdateSettings for the
// lock order and the rules for code that runs under it.
var settingsWriteMu sync.Mutex

// settingsRevisionCounter hands out the revisions stamped on published
// snapshots. It is process-wide and only ever grows, and it is never derived from
// a snapshot's own revision, so a rollback or a concurrent publish cannot make
// two different contents share a revision. The first value handed out is 1;
// 0 means "not stamped".
var settingsRevisionCounter atomic.Uint64

// nextSettingsRevision returns a revision that no snapshot in this process has
// carried before.
func nextSettingsRevision() uint64 { return settingsRevisionCounter.Add(1) }

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
// The published snapshot carries a new revision (Settings.Revision), stamped
// after mutate returns. The runtime-only range filter writers in this package
// publish without bumping it.
//
// Lock order: the writer lock is acquired after the model manager's mm.mu
// (ModelManager.Uninstall holds mm.mu while its config writer calls
// UpdateSettings) and the API controller's settingsMutex, and is never held while
// acquiring either. The save lock taken by SaveSettings is a leaf.
func UpdateSettings(mutate func(current, updated *Settings) error) (current, updated *Settings, err error) {
	return updateSettings(mutate, true)
}

// updateRuntimeSettings publishes like UpdateSettings but keeps current's
// revision. It is only for writers that change server-owned runtime fields a
// client can never write (the range filter species list, its lookup set and its
// update stamp): anything else must use UpdateSettings, or an If-Match check
// would accept a request built on a state it never saw.
func updateRuntimeSettings(mutate func(current, updated *Settings) error) (current, updated *Settings, err error) {
	return updateSettings(mutate, false)
}

// updateSettings is the body of UpdateSettings and updateRuntimeSettings. The
// revision is set after mutate returns, so a mutate that overwrites *updated
// wholesale cannot leak a stale value into the published snapshot.
func updateSettings(mutate func(current, updated *Settings) error, bump bool) (current, updated *Settings, err error) {
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
	if bump {
		updated.revision = nextSettingsRevision()
	} else {
		updated.revision = current.revision
	}
	settingsInstance.Store(updated)
	return current, updated, nil
}

// CompareAndStoreSettings restores next as the published snapshot only if old is
// still the published snapshot, and reports whether it did. It is how a writer
// that published old earlier takes its own change back without removing a
// snapshot another writer published on top of it: when it returns false the
// newer snapshot is left in place and the caller must say so rather than claim a
// rollback.
//
// What is published is a clone of next stamped with a fresh revision, returned as
// published. next keeps the revision it had, which an ETag may still carry, and
// old's revision was handed out while old was current, so reusing either would
// let an If-Match for the rolled-back state match the restored one. The clone
// also keeps the rule that published snapshots share nothing with a snapshot a
// caller still holds.
//
// It serializes with UpdateSettings, so it can be called from any context that
// UpdateSettings can, and it must not be called from inside a mutate function.
// Passing a nil old matches only an unloaded state and is not useful; a nil next
// is never published.
func CompareAndStoreSettings(old, next *Settings) (published *Settings, ok bool) {
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()
	if next == nil || settingsInstance.Load() != old {
		return nil, false
	}
	restored := CloneSettings(next)
	restored.revision = nextSettingsRevision()
	settingsInstance.Store(restored)
	return restored, true
}
