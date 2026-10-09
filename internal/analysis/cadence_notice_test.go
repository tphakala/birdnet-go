package analysis

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// fakeCadenceNoticeService records creates and deletes.
type fakeCadenceNoticeService struct {
	mu      sync.Mutex
	created []*notification.Notification
	deleted []string
}

func (f *fakeCadenceNoticeService) CreateWithMetadata(n *notification.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, n)
	return nil
}

func (f *fakeCadenceNoticeService) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeCadenceNoticeService) counts() (created, deleted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created), len(f.deleted)
}

func (f *fakeCadenceNoticeService) last() *notification.Notification {
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
	svc := &fakeCadenceNoticeService{}
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
	svc := &fakeCadenceNoticeService{}
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
	svc := &fakeCadenceNoticeService{}
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
		svc := &fakeCadenceNoticeService{}
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
	svc := &fakeCadenceNoticeService{}
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
	svc := &fakeCadenceNoticeService{}
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
	svc := &fakeCadenceNoticeService{}
	n := newTestCadenceNotice(svc)

	n.observe(nil)
	n.observe(noticePlan(cadence.StatusCapped, 2800*time.Millisecond, 1800*time.Millisecond))

	created, _ := svc.counts()
	assert.Equal(t, 1, created, "a nil plan does not count as the first plan")
}
