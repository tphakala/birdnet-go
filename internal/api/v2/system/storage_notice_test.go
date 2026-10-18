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

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health/checks"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/notification/mocks"
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

// noticeHarness is a handler whose storage report is injected.
type noticeHarness struct {
	h      *Handler
	report atomic.Pointer[checks.StoragePersistenceReport]
	calls  atomic.Int32
}

func newNoticeHarness(svc notification.NoticeService) *noticeHarness {
	nh := &noticeHarness{h: &Handler{Core: &apicore.Core{}}}
	nh.h.storage.service = func() notification.NoticeService { return svc }
	nh.h.storage.report = func() checks.StoragePersistenceReport {
		nh.calls.Add(1)
		return *nh.report.Load()
	}
	nh.set(checks.StoragePersistenceReport{Applicable: true})
	return nh
}

func (nh *noticeHarness) set(r checks.StoragePersistenceReport) { nh.report.Store(&r) }

// verdict builds a verdict whose resolved path is /<label>.
func verdict(label string, class diagnostics.PersistenceClass) diagnostics.StorageVerdict {
	return diagnostics.StorageVerdict{Label: label, Path: "/" + label, ResolvedPath: "/" + label, Class: class}
}

func applicable(vs ...diagnostics.StorageVerdict) checks.StoragePersistenceReport {
	return checks.StoragePersistenceReport{Applicable: true, Verdicts: vs}
}

const (
	classLayer = diagnostics.PersistenceContainerLayer
	classOK    = diagnostics.PersistencePersistent
	classAnon  = diagnostics.PersistenceAnonymousVolume
)

func TestStorageNotice_Transitions(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 4, 3)
	nh := newNoticeHarness(rec)
	step := func(r checks.StoragePersistenceReport, wantCreated, wantDeleted int) {
		t.Helper()
		nh.set(r)
		nh.h.syncStorageNotices()
		created, deleted := rec.counts()
		assert.Equal(t, wantCreated, created, "created")
		assert.Equal(t, wantDeleted, deleted, "deleted")
	}

	// 1. everything persistent
	step(applicable(verdict("config", classOK), verdict("models", classOK)), 0, 0)

	// 2. models lost: a medium priority notice
	step(applicable(verdict("config", classOK), verdict("models", classLayer)), 1, 0)
	n := rec.last()
	require.NotNil(t, n)
	assert.Equal(t, notification.PriorityMedium, n.Priority)
	assert.Equal(t, notification.ComponentStorage, n.Component)
	assert.Equal(t, notification.MsgStorageNotPersistentTitle, n.TitleKey)
	assert.Equal(t, notification.MsgStorageNotPersistentMessage, n.MessageKey)
	assert.Equal(t, "/models", n.MessageParams["paths"])
	assert.Equal(t, notification.DeliveryTargetBell, n.DeliveryTarget)

	// 3. the same again: nothing happens
	step(applicable(verdict("config", classOK), verdict("models", classLayer)), 1, 0)

	// 4. database joins: replaced, high priority
	step(applicable(verdict("database", classLayer), verdict("models", classLayer)), 2, 1)
	n = rec.last()
	assert.Equal(t, notification.PriorityHigh, n.Priority)
	assert.Equal(t, "/database, /models", n.MessageParams["paths"])

	// 5. models only again: replaced
	step(applicable(verdict("database", classOK), verdict("models", classLayer)), 3, 2)
	assert.Equal(t, notification.PriorityMedium, rec.last().Priority)

	// 6. all persistent: deleted
	step(applicable(verdict("database", classOK), verdict("models", classOK)), 3, 3)

	// 7. models lost again: raised again
	step(applicable(verdict("models", classLayer)), 4, 3)
}

func TestStorageNotice_DeletedByUserNotReraisedForSameSignature(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 0)
	nh := newNoticeHarness(rec)
	nh.set(applicable(verdict("models", classLayer)))

	nh.h.syncStorageNotices()
	nh.h.syncStorageNotices()

	created, _ := rec.counts()
	assert.Equal(t, 1, created)
}

func TestStorageNotice_ReadErrorKeepsLatch(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 1, 0)
	nh := newNoticeHarness(rec)
	lost := applicable(verdict("models", classLayer))
	nh.set(lost)
	nh.h.syncStorageNotices() // raise; the user may now delete it

	nh.set(checks.StoragePersistenceReport{Applicable: true, Err: errors.NewStd("mountinfo unreadable")})
	nh.h.syncStorageNotices() // no Delete, no Create

	nh.set(lost)
	nh.h.syncStorageNotices() // same signature as before: no Create

	created, deleted := rec.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 0, deleted)
}

func TestStorageNotice_LatchesIndependent(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 2, 1)
	nh := newNoticeHarness(rec)
	nh.set(applicable(verdict("models", classLayer), verdict("config", classAnon)))
	nh.h.syncStorageNotices()

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
	assert.Equal(t, "/config", anonNotice.MessageParams["paths"])

	// The anonymous set resolves: only the anonymous notice goes.
	nh.set(applicable(verdict("models", classLayer), verdict("config", classOK)))
	nh.h.syncStorageNotices()

	_, deleted = rec.counts()
	assert.Equal(t, 1, deleted)
	rec.mu.Lock()
	assert.Equal(t, []string{anonNotice.ID}, rec.deleted)
	rec.mu.Unlock()
}

func TestStorageNotice_SignatureOrderIndependent(t *testing.T) {
	t.Parallel()
	a := []diagnostics.StorageVerdict{verdict("models", classLayer), verdict("database", classLayer)}
	b := []diagnostics.StorageVerdict{verdict("database", classLayer), verdict("models", classLayer)}
	assert.Equal(t, storageSignature(a), storageSignature(b))
	assert.Empty(t, storageSignature(nil))
	assert.NotEqual(t, storageSignature(a), storageSignature(a[:1]))
}

func TestStorageNotice_NotApplicableClearsAndRaisesNothing(t *testing.T) {
	rec := newStorageNoticeRecorder(t, 0, 0)
	nh := newNoticeHarness(rec)
	nh.set(checks.StoragePersistenceReport{})

	nh.h.syncStorageNotices()

	created, deleted := rec.counts()
	assert.Zero(t, created)
	assert.Zero(t, deleted)
}

func TestStorageNotice_StartFiresOnceAndStopPreventsLaterSync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newStorageNoticeRecorder(t, 0, 0)
		nh := newNoticeHarness(rec)

		nh.h.StartStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()
		nh.h.Wait()
		assert.Equal(t, int32(1), nh.calls.Load(), "the startup evaluation runs once")

		nh.h.ScheduleStorageNoticeSync()
		nh.h.StopStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()
		assert.Equal(t, int32(1), nh.calls.Load(), "Stop cancels the pending evaluation")

		nh.h.ScheduleStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()
		assert.Equal(t, int32(1), nh.calls.Load(), "a schedule after Stop arms nothing")
	})
}

func TestStorageNotice_ScheduleBeforeStartIsNoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newStorageNoticeRecorder(t, 0, 0)
		nh := newNoticeHarness(rec)

		nh.h.ScheduleStorageNoticeSync()
		time.Sleep(storageNoticeDebounce + time.Second)
		synctest.Wait()

		assert.Zero(t, nh.calls.Load())
	})
}
