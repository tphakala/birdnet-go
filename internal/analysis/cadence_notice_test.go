package analysis

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/notification/mocks"
)

// cadenceNoticeRecorder wraps the generated NoticeService mock and records the
// notices created and deleted through it.
type cadenceNoticeRecorder struct {
	*mocks.MockNoticeService
	mu        sync.Mutex
	created   []*notification.Notification
	deleted   []string
	createErr error
}

func newCadenceNoticeRecorder(t *testing.T) *cadenceNoticeRecorder {
	t.Helper()
	f := &cadenceNoticeRecorder{MockNoticeService: mocks.NewMockNoticeService(t)}
	f.EXPECT().CreateWithMetadata(mock.Anything).RunAndReturn(func(n *notification.Notification) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.createErr != nil {
			return f.createErr
		}
		f.created = append(f.created, n)
		return nil
	}).Maybe()
	f.EXPECT().Delete(mock.Anything).RunAndReturn(func(id string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.deleted = append(f.deleted, id)
		return nil
	}).Maybe()
	return f
}

func (f *cadenceNoticeRecorder) setCreateErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createErr = err
}

func (f *cadenceNoticeRecorder) counts() (created, deleted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created), len(f.deleted)
}

func (f *cadenceNoticeRecorder) last() *notification.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) == 0 {
		return nil
	}
	return f.created[len(f.created)-1]
}

func noticePlan(status cadence.Status, configured, effective time.Duration) *cadence.Plan {
	return &cadence.Plan{
		Status:                status,
		ConfiguredBaseOverlap: configured,
		EffectiveBaseOverlap:  effective,
		SourceCount:           1,
		ModelCount:            2,
		UnknownLatencyModels:  []string{},
	}
}

func newTestCadenceNotice(svc notification.NoticeService) *cadenceNotice {
	return &cadenceNotice{service: func() notification.NoticeService { return svc }}
}

func TestCadenceNotice_FirstPlanCappedRaisesOnce(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	n := newTestCadenceNotice(svc)

	capped := noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond)
	n.observe(capped)
	n.observe(capped)

	created, deleted := svc.counts()
	assert.Equal(t, 1, created, "the same capped plan raises one notice")
	assert.Zero(t, deleted)

	notif := svc.last()
	require.NotNil(t, notif)
	assert.Equal(t, notification.MsgCadenceCappedTitle, notif.TitleKey)
	assert.Equal(t, notification.MsgCadenceCappedMessage, notif.MessageKey)
	assert.Equal(t, notification.TypeWarning, notif.Type)
	assert.Equal(t, notification.DeliveryTargetBell, notif.DeliveryTarget)
	assert.Equal(t, "2.8", notif.MessageParams["configured"])
	assert.Equal(t, "1.8", notif.MessageParams["effective"])
	assert.Equal(t, 2, notif.MessageParams["models"])
	assert.Equal(t, 1, notif.MessageParams["sources"])
	assert.Contains(t, notif.Message, "1.8 s")
}

func TestCadenceNotice_ChangedEffectiveReplaces(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	n := newTestCadenceNotice(svc)

	n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond))
	n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1500*time.Millisecond))

	created, deleted := svc.counts()
	assert.Equal(t, 2, created, "a changed effective overlap replaces the notice")
	assert.Equal(t, 1, deleted)
	assert.Equal(t, "1.5", svc.last().MessageParams["effective"])
}

func TestCadenceNotice_OverloadedUsesOverloadedKeys(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	n := newTestCadenceNotice(svc)

	n.observe(noticePlan(cadence.StatusOverloaded, 2800*time.Millisecond, 0))

	notif := svc.last()
	require.NotNil(t, notif)
	assert.Equal(t, notification.MsgCadenceOverloadedTitle, notif.TitleKey)
	assert.Equal(t, notification.MsgCadenceOverloadedMessage, notif.MessageKey)
	assert.Equal(t, notification.PriorityHigh, notif.Priority)
}

func TestCadenceNotice_FirstPlanOKNeverRaises(t *testing.T) {
	t.Parallel()
	for _, first := range []cadence.Status{cadence.StatusOK, cadence.StatusFilterOff} {
		svc := newCadenceNoticeRecorder(t)
		n := newTestCadenceNotice(svc)

		n.observe(noticePlan(first, 2000*time.Millisecond, 2000*time.Millisecond))
		// A later capped plan is shown inline in the settings page; the bell
		// only reports the state the process started in.
		n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond))

		created, deleted := svc.counts()
		assert.Zero(t, created, "first plan %s raises nothing", first)
		assert.Zero(t, deleted)
	}
}

func TestCadenceNotice_ResolvedClearsAndNeverReraises(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	n := newTestCadenceNotice(svc)

	n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond))
	n.observe(noticePlan(cadence.StatusOK, 1500*time.Millisecond, 1500*time.Millisecond))

	created, deleted := svc.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, deleted, "a plan that is no longer capped clears the notice")

	n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond))
	created, _ = svc.counts()
	assert.Equal(t, 1, created, "once cleared, the notice is not raised again in this process")
}

func TestCadenceNotice_NoServiceRaisesOnNextPlan(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	var current notification.NoticeService
	n := &cadenceNotice{service: func() notification.NoticeService { return current }}

	capped := noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond)
	n.observe(capped) // notification service not up yet
	current = svc
	n.observe(capped)

	created, _ := svc.counts()
	assert.Equal(t, 1, created, "the first plan's notice is raised once the service is available")
}

func TestCadenceNotice_NilPlanIgnored(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	n := newTestCadenceNotice(svc)

	n.observe(nil)
	n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond))

	created, _ := svc.counts()
	assert.Equal(t, 1, created, "a nil plan does not count as the first plan")
}

func TestCadenceNotice_ChangedInputsReplace(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	n := newTestCadenceNotice(svc)

	n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond))
	n.observe(noticePlan(cadence.StatusCapped, 2500*time.Millisecond, 1800*time.Millisecond))
	created, deleted := svc.counts()
	assert.Equal(t, 2, created, "a new configured overlap the text states replaces the notice")
	assert.Equal(t, 1, deleted)
	assert.Equal(t, "2.5", svc.last().MessageParams["configured"])

	more := noticePlan(cadence.StatusCapped, 2500*time.Millisecond, 1800*time.Millisecond)
	more.SourceCount = 2
	n.observe(more)
	created, _ = svc.counts()
	assert.Equal(t, 3, created, "a new source count the text states replaces the notice")
	assert.Equal(t, 2, svc.last().MessageParams["sources"])
}

func TestCadenceNotice_OverloadedIgnoresConfiguredOverlap(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	n := newTestCadenceNotice(svc)

	n.observe(noticePlan(cadence.StatusOverloaded, 2800*time.Millisecond, 0))
	n.observe(noticePlan(cadence.StatusOverloaded, 2400*time.Millisecond, 0))
	created, deleted := svc.counts()
	assert.Equal(t, 1, created, "the overloaded text does not state the configured overlap")
	assert.Zero(t, deleted)

	fewer := noticePlan(cadence.StatusOverloaded, 2400*time.Millisecond, 0)
	fewer.ModelCount = 1
	n.observe(fewer)
	created, _ = svc.counts()
	assert.Equal(t, 2, created, "a new model count the text states replaces the notice")
	assert.Equal(t, 1, svc.last().MessageParams["models"])
}

func TestCadenceNotice_RetryRaisesAfterFailedCreate(t *testing.T) {
	t.Parallel()
	svc := newCadenceNoticeRecorder(t)
	svc.setCreateErr(errors.NewStd("rate limited"))
	n := newTestCadenceNotice(svc)

	n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond))
	created, _ := svc.counts()
	require.Zero(t, created, "the first create fails")

	svc.setCreateErr(nil)
	n.retry()
	created, _ = svc.counts()
	assert.Equal(t, 1, created, "the retry raises the notice for the first plan")
	assert.NotEmpty(t, n.latch.ID())
}
