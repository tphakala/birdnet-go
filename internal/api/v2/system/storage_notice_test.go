package system

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/notification/mocks"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// storageNoticeRecorder wraps the generated NoticeService mock and records the
// notices created and deleted through it.
type storageNoticeRecorder struct {
	*mocks.MockNoticeService
	mu      sync.Mutex
	created []*notification.Notification
	deleted []string
}

// newStorageNoticeRecorder expects exactly creates create calls and deletes
// delete calls over the whole test.
func newStorageNoticeRecorder(t *testing.T, creates, deletes int) *storageNoticeRecorder {
	t.Helper()
	r := &storageNoticeRecorder{MockNoticeService: mocks.NewMockNoticeService(t)}
	if creates > 0 {
		r.EXPECT().CreateWithMetadata(mock.Anything).RunAndReturn(func(n *notification.Notification) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.created = append(r.created, n)
			return nil
		}).Times(creates)
	}
	if deletes > 0 {
		r.EXPECT().Delete(mock.Anything).RunAndReturn(func(id string) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.deleted = append(r.deleted, id)
			return nil
		}).Times(deletes)
	}
	return r
}

func (r *storageNoticeRecorder) counts() (created, deleted int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.created), len(r.deleted)
}

func (r *storageNoticeRecorder) last() *notification.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.created) == 0 {
		return nil
	}
	return r.created[len(r.created)-1]
}

const (
	classLayer = diagnostics.PersistenceContainerLayer
	classOK    = diagnostics.PersistencePersistent
	classAnon  = diagnostics.PersistenceAnonymousVolume
	classUnk   = diagnostics.PersistenceUnknown
)

// newNoticeFixture returns a storage fixture whose notices go to svc.
func newNoticeFixture(svc notification.NoticeService) *storageFixture {
	f := newStorageFixture()
	f.h.notices = svc
	return f
}

// sync runs one evaluation.
func (f *storageFixture) sync() { f.h.syncStorageNotices() }

func TestStorageNotice_Transitions(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 4, 3)
	f := newNoticeFixture(rec)
	step := func(wantCreated, wantDeleted int) {
		t.Helper()
		f.sync()
		created, deleted := rec.counts()
		assert.Equal(t, wantCreated, created, "created")
		assert.Equal(t, wantDeleted, deleted, "deleted")
	}

	// 1. everything persistent
	step(0, 0)

	// 2. models lost: a medium priority notice
	f.set("models", classLayer)
	step(1, 0)
	n := rec.last()
	require.NotNil(t, n)
	assert.Equal(t, notification.PriorityMedium, n.Priority)
	assert.Equal(t, notification.ComponentStorage, n.Component)
	assert.Equal(t, notification.MsgStorageNotPersistentTitle, n.TitleKey)
	assert.Equal(t, notification.MsgStorageNotPersistentMessage, n.MessageKey)
	assert.Equal(t, "/models", n.MessageParams["paths"])
	assert.Equal(t, notification.DeliveryTargetBell, n.DeliveryTarget)

	// 3. the same again: nothing happens
	step(1, 0)

	// 4. database joins: replaced, high priority
	f.set("database", classLayer)
	step(2, 1)
	n = rec.last()
	assert.Equal(t, notification.PriorityHigh, n.Priority)
	assert.Equal(t, "/data/birdnet.db, /models", n.MessageParams["paths"])

	// 5. models only again: replaced
	f.set("database", classOK)
	step(3, 2)
	assert.Equal(t, notification.PriorityMedium, rec.last().Priority)

	// 6. all persistent: deleted
	f.set("models", classOK)
	step(3, 3)

	// 7. models lost again: raised again
	f.set("models", classLayer)
	step(4, 3)
}

func TestStorageNotice_MemoryBackedPathIsLost(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 0)
	f := newNoticeFixture(rec)
	f.set("models", diagnostics.PersistenceMemory)

	f.sync()

	require.NotNil(t, rec.last())
	assert.Equal(t, notification.MsgStorageNotPersistentTitle, rec.last().TitleKey)
}

func TestStorageNotice_ReadErrorKeepsLatch(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 0)
	f := newNoticeFixture(rec)
	f.set("models", classLayer)
	f.sync() // raise; the user may now delete it

	f.setReadErr(errors.NewStd("mountinfo unreadable"))
	f.sync() // no Delete, no Create

	f.setReadErr(nil)
	f.sync() // same signature as before: no Create

	created, deleted := rec.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 0, deleted)
}

func TestStorageNotice_LatchesIndependent(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 2, 1)
	f := newNoticeFixture(rec)
	f.set("models", classLayer)
	f.set("config", classAnon)
	f.sync()

	created, deleted := rec.counts()
	require.Equal(t, 2, created)
	require.Equal(t, 0, deleted)
	byKey := map[string]*notification.Notification{}
	rec.mu.Lock()
	for _, n := range rec.created {
		byKey[n.TitleKey] = n
	}
	rec.mu.Unlock()
	lostNotice := byKey[notification.MsgStorageNotPersistentTitle]
	anonNotice := byKey[notification.MsgStorageAnonymousVolumeTitle]
	require.NotNil(t, lostNotice)
	require.NotNil(t, anonNotice)
	assert.Equal(t, notification.PriorityLow, anonNotice.Priority)
	assert.Equal(t, notification.MsgStorageAnonymousVolumeMessage, anonNotice.MessageKey)
	assert.Equal(t, "/config/config.yaml", anonNotice.MessageParams["paths"])

	// The anonymous set resolves: only the anonymous notice goes.
	f.set("config", classOK)
	f.sync()

	_, deleted = rec.counts()
	assert.Equal(t, 1, deleted)
	rec.mu.Lock()
	assert.Equal(t, []string{anonNotice.ID}, rec.deleted)
	rec.mu.Unlock()
}

func TestStorageNotice_SignatureOrderIndependent(t *testing.T) {
	t.Parallel()
	mk := func(label string) diagnostics.StorageVerdict {
		return diagnostics.StorageVerdict{Label: label, ResolvedPath: "/" + label, Class: classLayer}
	}
	a := []diagnostics.StorageVerdict{mk("models"), mk("database")}
	b := []diagnostics.StorageVerdict{mk("database"), mk("models")}
	assert.Equal(t, storageSignature(a), storageSignature(b))
	assert.Empty(t, storageSignature(nil))
	assert.NotEqual(t, storageSignature(a), storageSignature(a[:1]))
}

func TestStorageNotice_NotApplicableClearsAndRaisesNothing(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 0, 0)
	f := newNoticeFixture(rec)
	f.set("models", classLayer)
	f.setEnv(sysinfo.EnvLXC)

	f.sync()

	created, deleted := rec.counts()
	assert.Zero(t, created)
	assert.Zero(t, deleted)
}

func TestStorageNotice_NotApplicableClearsLiveNotice(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 1)
	f := newNoticeFixture(rec)
	f.set("models", classLayer)
	f.sync()

	f.setEnv(sysinfo.EnvLXC)
	f.sync()

	_, deleted := rec.counts()
	assert.Equal(t, 1, deleted)
}

func TestStorageNotice_UnknownVerdictKeepsLatch(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 1)
	f := newNoticeFixture(rec)
	f.set("models", classLayer)
	f.sync()

	// The path can no longer be judged (a resolve error): the live notice stays.
	f.set("models", classUnk)
	f.sync()

	created, deleted := rec.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 0, deleted, "an unknown verdict is not proof that the problem went away")

	// Judged persistent again: now the notice goes.
	f.set("models", classOK)
	f.sync()

	_, deleted = rec.counts()
	assert.Equal(t, 1, deleted)
}

func TestStorageNotice_UnknownVerdictKeepsSeveralPathsNotice(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 2, 1)
	f := newNoticeFixture(rec)
	f.set("models", classLayer)
	f.set("clips", classLayer)
	f.sync()
	require.Equal(t, "/models, /clips", rec.last().MessageParams["paths"])

	// One of the two paths cannot be judged: the notice keeps naming both and
	// is not replaced, so a notice the user deleted does not come back.
	f.set("clips", classUnk)
	f.sync()

	created, deleted := rec.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 0, deleted)

	// Once clips is judged persistent the notice names only models.
	f.set("clips", classOK)
	f.sync()

	created, deleted = rec.counts()
	assert.Equal(t, 2, created)
	assert.Equal(t, 1, deleted)
	assert.Equal(t, "/models", rec.last().MessageParams["paths"])
}

func TestStorageNotice_UnrelatedUnknownVerdictDoesNotKeepNotice(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 1)
	f := newNoticeFixture(rec)
	f.set("config", classLayer)
	f.sync()

	// The config is fixed while the clips path stays unjudged.
	f.set("config", classOK)
	f.set("clips", classUnk)
	f.sync()

	_, deleted := rec.counts()
	assert.Equal(t, 1, deleted, "an unrelated unjudged path does not hold a notice")
}

func TestStorageNotice_UnknownVerdictWithoutHistoryRaisesNothing(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 0, 0)
	f := newNoticeFixture(rec)
	f.set("models", classUnk)

	f.sync()

	created, deleted := rec.counts()
	assert.Zero(t, created)
	assert.Zero(t, deleted)
}

func TestStorageNotice_ChangedPathIsNotCarried(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 1)
	f := newNoticeFixture(rec)
	f.set("models", classLayer)
	f.sync()

	// The models directory is reconfigured and the new one cannot be judged: the
	// old verdict belonged to another path and is dropped.
	f.update(func(s *conf.Settings) { s.Models.Directory = "/other-models" })
	f.set("models", classUnk)
	f.sync()

	_, deleted := rec.counts()
	assert.Equal(t, 1, deleted)
}

func TestStorageNotice_UnknownVerdictDoesNotBlockOtherPaths(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 2, 0)
	f := newNoticeFixture(rec)
	f.set("clips", classUnk)
	f.set("models", classLayer)
	f.set("config", classAnon)

	f.sync()

	created, deleted := rec.counts()
	assert.Equal(t, 2, created, "a lost path and an anonymous path are still raised beside an unjudged one")
	assert.Zero(t, deleted)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	titles := []string{rec.created[0].TitleKey, rec.created[1].TitleKey}
	assert.ElementsMatch(t, []string{
		notification.MsgStorageNotPersistentTitle,
		notification.MsgStorageAnonymousVolumeTitle,
	}, titles)
}

func TestStorageNotice_StartFiresOnceAndStopPreventsLaterSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newNoticeFixture(newStorageNoticeRecorder(t, 0, 0))

		f.h.StartStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()
		f.h.Wait()
		assert.Equal(t, int32(1), f.reports.Load(), "the startup evaluation runs once")

		f.h.ScheduleStorageNoticeSync()
		f.h.StopStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()
		assert.Equal(t, int32(1), f.reports.Load(), "Stop cancels the pending evaluation")

		f.h.ScheduleStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()
		assert.Equal(t, int32(1), f.reports.Load(), "a schedule after Stop arms nothing")
	})
}

func TestStorageNotice_ScheduleBeforeStartIsNoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newNoticeFixture(newStorageNoticeRecorder(t, 0, 0))

		f.h.ScheduleStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()

		assert.Zero(t, f.reports.Load())
	})
}

func TestStorageNotice_ScheduleRunsAfterStartAndDebounces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newNoticeFixture(newStorageNoticeRecorder(t, 0, 0))

		f.h.StartStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()
		require.Equal(t, int32(1), f.reports.Load(), "the startup evaluation")

		// Two schedules inside the window run one evaluation, after the second.
		f.h.ScheduleStorageNoticeSync()
		time.Sleep(time.Second)
		f.h.ScheduleStorageNoticeSync()
		time.Sleep(storageNoticeDebounce - time.Second)
		synctest.Wait()
		assert.Equal(t, int32(1), f.reports.Load(), "the second schedule restarted the debounce")

		time.Sleep(2 * time.Second)
		synctest.Wait()
		assert.Equal(t, int32(2), f.reports.Load(), "the coalesced evaluation ran once")

		f.h.StopStorageNoticeSync()
		f.h.Wait()
	})
}

func TestStorageNotice_FailedCreateIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := mocks.NewMockNoticeService(t)
		var attempts atomic.Int32
		svc.EXPECT().CreateWithMetadata(mock.Anything).RunAndReturn(func(*notification.Notification) error {
			if attempts.Add(1) == 1 {
				return errors.NewStd("rate limited")
			}
			return nil
		}).Times(2)
		f := newNoticeFixture(svc)
		f.set("models", classLayer)

		f.h.StartStorageNoticeSync()
		time.Sleep(2 * time.Minute)
		synctest.Wait()

		assert.Equal(t, int32(2), attempts.Load(), "the failed raise is retried through the latch")
		f.h.StopStorageNoticeSync()
		f.h.Wait()
	})
}

func TestRegisterHealthChecks_IncludesStoragePersistence(t *testing.T) {
	h := &Handler{Core: apitest.NewCore(t)}
	h.healthRegistry = health.NewRegistry()
	h.storageDeps = storageDeps{envType: func() string { return sysinfo.EnvLXC }}
	h.registerHealthChecks()

	var found *health.Result
	results := h.healthRegistry.RunCategory(t.Context(), health.CategoryConfig)
	for i := range results {
		if results[i].Name == "storage_persistence" {
			found = &results[i]
		}
	}

	require.NotNil(t, found, "the check is registered in the config category")
	assert.Equal(t, health.StatusSkipped, found.Status, "outside Docker and Podman the check is skipped")
}

func TestStorageNotice_FixedPathIsForgottenNotCarried(t *testing.T) {
	t.Run("judged persistent then unknown", func(t *testing.T) {
		rec := newStorageNoticeRecorder(t, 1, 1)
		f := newNoticeFixture(rec)
		f.set("models", classLayer)
		f.sync()
		f.set("models", classOK)
		f.sync()

		f.set("models", classUnk)
		f.sync()

		created, deleted := rec.counts()
		assert.Equal(t, 1, created, "the old lost verdict is not carried over a persistent one")
		assert.Equal(t, 1, deleted)
	})

	t.Run("not applicable then applicable and unknown", func(t *testing.T) {
		rec := newStorageNoticeRecorder(t, 1, 1)
		f := newNoticeFixture(rec)
		f.set("models", classLayer)
		f.sync()
		f.setEnv(sysinfo.EnvLXC)
		f.sync()

		f.setEnv(sysinfo.EnvDocker)
		f.set("models", classUnk)
		f.sync()

		created, deleted := rec.counts()
		assert.Equal(t, 1, created, "leaving the container context forgets the verdict")
		assert.Equal(t, 1, deleted)
	})

	t.Run("label disappears then returns unknown", func(t *testing.T) {
		rec := newStorageNoticeRecorder(t, 1, 1)
		f := newNoticeFixture(rec)
		f.set("clips", classLayer)
		f.sync()
		f.update(func(s *conf.Settings) { s.Realtime.Audio.Export.Enabled = false })
		f.sync()

		f.update(func(s *conf.Settings) { s.Realtime.Audio.Export.Enabled = true })
		f.set("clips", classUnk)
		f.sync()

		created, deleted := rec.counts()
		assert.Equal(t, 1, created, "a label the report stopped listing is forgotten")
		assert.Equal(t, 1, deleted)
	})
}

func TestSyncStorageNotices_RetriesWhenServiceMissing(t *testing.T) {
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)

	// raised counts the notices in the process-wide service.
	raised := func() int {
		svc := notification.GetService()
		require.NotNil(t, svc)
		list, err := svc.List(nil)
		require.NoError(t, err)
		return len(list)
	}

	t.Run("a service that appears is found by a retry", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newStorageFixture()
			f.set("models", classLayer)

			f.h.StartStorageNoticeSync()
			time.Sleep(storageNoticeDebounce + time.Second) // first evaluation: no service
			synctest.Wait()
			notification.Initialize(notification.DefaultServiceConfig())
			defer notification.GetService().Stop()
			time.Sleep(storageNoticeDebounce) // the retry
			synctest.Wait()

			assert.Equal(t, 1, raised(), "the retry raised the notice once the service existed")
			f.h.StopStorageNoticeSync()
			f.h.Wait()
		})
		notification.ResetForTest()
	})

	t.Run("retries stop when no service ever appears", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newStorageFixture()
			f.set("models", classLayer)

			f.h.StartStorageNoticeSync()
			time.Sleep(time.Hour) // far beyond every bounded retry
			synctest.Wait()
			notification.Initialize(notification.DefaultServiceConfig())
			defer notification.GetService().Stop()
			time.Sleep(time.Hour)
			synctest.Wait()

			assert.Zero(t, raised(), "the retries are bounded")

			f.h.ScheduleStorageNoticeSync() // an external trigger starts over
			time.Sleep(storageNoticeDebounce + time.Second)
			synctest.Wait()
			assert.Equal(t, 1, raised(), "an external schedule finds the service")
			f.h.StopStorageNoticeSync()
			f.h.Wait()
		})
		notification.ResetForTest()
	})
}

// TestSyncStorageNotices_RetryDelaysDouble pins that each retry waits twice as
// long as the one before it. A service that appears right after the second
// evaluation must not be found by a retry at the plain debounce delay.
func TestSyncStorageNotices_RetryDelaysDouble(t *testing.T) {
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)

	synctest.Test(t, func(t *testing.T) {
		f := newStorageFixture()
		f.set("models", classLayer)

		f.h.StartStorageNoticeSync()
		// Evaluations run at 3 s and 6 s without a service; the third is due at 12 s.
		time.Sleep(storageNoticeDebounce*2 + time.Second)
		synctest.Wait()
		notification.Initialize(notification.DefaultServiceConfig())
		defer notification.GetService().Stop()

		raised := func() int {
			list, err := notification.GetService().List(nil)
			require.NoError(t, err)
			return len(list)
		}
		time.Sleep(storageNoticeDebounce) // 10 s: a constant delay would have retried at 9 s
		synctest.Wait()
		assert.Zero(t, raised(), "the third evaluation is not due before the doubled delay")

		time.Sleep(storageNoticeDebounce) // 13 s
		synctest.Wait()
		assert.Equal(t, 1, raised(), "the doubled retry found the service")
		f.h.StopStorageNoticeSync()
		f.h.Wait()
	})
	notification.ResetForTest()
}

// TestSyncStorageNotices_RetryBudgetRestarts pins that an external schedule and a
// found service both start the retry budget over.
func TestSyncStorageNotices_RetryBudgetRestarts(t *testing.T) {
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)

	synctest.Test(t, func(t *testing.T) {
		f := newStorageFixture()
		f.set("models", classLayer)

		f.h.StartStorageNoticeSync()
		time.Sleep(time.Hour) // the budget is spent
		synctest.Wait()

		f.h.ScheduleStorageNoticeSync() // starts the budget over
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait() // evaluation without a service arms a fresh retry
		notification.Initialize(notification.DefaultServiceConfig())
		defer notification.GetService().Stop()
		time.Sleep(storageNoticeDebounce)
		synctest.Wait()

		list, err := notification.GetService().List(nil)
		require.NoError(t, err)
		assert.Len(t, list, 1, "a schedule after the spent budget still retries")

		f.h.storage.timerMu.Lock()
		retries := f.h.storage.noServiceRetries
		f.h.storage.timerMu.Unlock()
		assert.Zero(t, retries, "finding the service starts the budget over")
		f.h.StopStorageNoticeSync()
		f.h.Wait()
	})
	notification.ResetForTest()
}
