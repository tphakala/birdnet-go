package notification

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakeNoticeService records creates and deletes and can be told to fail them.
type fakeNoticeService struct {
	mu        sync.Mutex
	created   []*Notification
	deleted   []string
	createErr error
	deleteErr error
}

func (f *fakeNoticeService) CreateWithMetadata(n *Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, n)
	return nil
}

func (f *fakeNoticeService) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeNoticeService) counts() (created, deleted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created), len(f.deleted)
}

// want returns a compute func that asks for sig with a notice titled after it.
func want(sig string) func() (string, func() *Notification) {
	return func() (string, func() *Notification) {
		return sig, func() *Notification {
			return NewNotification(TypeError, PriorityHigh, "title "+sig, "message "+sig)
		}
	}
}

func TestPersistentNotice_RaiseReplaceClear(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{}
	var p PersistentNotice

	require.NoError(t, p.Reconcile(svc, want("")))
	created, deleted := svc.counts()
	assert.Zero(t, created, "nothing due, nothing raised")
	assert.Zero(t, deleted)

	require.NoError(t, p.Reconcile(svc, want("a")))
	first := p.ID()
	require.NotEmpty(t, first)

	require.NoError(t, p.Reconcile(svc, want("a")))
	created, deleted = svc.counts()
	assert.Equal(t, 1, created, "an unchanged signature is a no-op")
	assert.Zero(t, deleted)

	require.NoError(t, p.Reconcile(svc, want("b")))
	created, deleted = svc.counts()
	assert.Equal(t, 2, created, "a changed signature replaces the notice")
	require.Equal(t, 1, deleted)
	assert.Equal(t, first, svc.deleted[0])
	assert.NotEqual(t, first, p.ID())

	require.NoError(t, p.Reconcile(svc, want("")))
	assert.Empty(t, p.ID(), "an empty signature clears the notice")
	_, deleted = svc.counts()
	assert.Equal(t, 2, deleted)
}

func TestPersistentNotice_DeleteErrorKeepsLatch(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{}
	var p PersistentNotice
	require.NoError(t, p.Reconcile(svc, want("a")))
	id := p.ID()

	svc.deleteErr = errors.NewStd("store down")
	require.Error(t, p.Reconcile(svc, want("")))
	assert.Equal(t, id, p.ID(), "a failed delete keeps the latch so the next trigger retries it")

	svc.deleteErr = nil
	require.NoError(t, p.Reconcile(svc, want("")))
	assert.Empty(t, p.ID())
}

func TestPersistentNotice_CreateErrorIsNotLatched(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{createErr: errors.NewStd("rate limit exceeded")}
	var p PersistentNotice
	require.Error(t, p.Reconcile(svc, want("a")))
	assert.Empty(t, p.ID())

	svc.createErr = nil
	require.NoError(t, p.Reconcile(svc, want("a")), "a later trigger retries the raise")
	assert.NotEmpty(t, p.ID())
}

func TestPersistentNotice_BoundedRetry(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{createErr: errors.NewStd("rate limit exceeded")}
	p := &PersistentNotice{retryDelay: time.Millisecond}

	var mu sync.Mutex
	retries := 0
	p.SetRetry(func() {
		mu.Lock()
		retries++
		mu.Unlock()
		_ = p.Reconcile(svc, want("a"))
	})

	require.Error(t, p.Reconcile(svc, want("a")))
	// The first failure plus persistentNoticeRetryMaxAttempts retries, then the
	// budget is spent and no further timer is armed.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return retries == persistentNoticeRetryMaxAttempts
	}, 5*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	assert.Equal(t, persistentNoticeRetryMaxAttempts, retries, "retries are bounded")
	mu.Unlock()
	assert.Empty(t, p.ID())
}

func TestPersistentNotice_RetryRaisesOnceServiceRecovers(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{createErr: errors.NewStd("rate limit exceeded")}
	p := &PersistentNotice{retryDelay: time.Millisecond}
	p.SetRetry(func() {
		svc.mu.Lock()
		svc.createErr = nil
		svc.mu.Unlock()
		_ = p.Reconcile(svc, want("a"))
	})

	require.Error(t, p.Reconcile(svc, want("a")))
	require.Eventually(t, func() bool { return p.ID() != "" }, 5*time.Second, time.Millisecond)
	created, _ := svc.counts()
	assert.Equal(t, 1, created)
}

func TestPersistentNotice_StopCancelsRetry(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{createErr: errors.NewStd("rate limit exceeded")}
	p := &PersistentNotice{retryDelay: 20 * time.Millisecond}
	var mu sync.Mutex
	retried := false
	p.SetRetry(func() {
		mu.Lock()
		retried = true
		mu.Unlock()
	})

	require.Error(t, p.Reconcile(svc, want("a")))
	p.Stop()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	assert.False(t, retried, "Stop cancels the pending retry")
	mu.Unlock()

	require.Error(t, p.Reconcile(svc, want("a")))
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	assert.False(t, retried, "no retry is armed after Stop")
	mu.Unlock()
}

func TestPersistentNotice_RecoveryCancelsRetry(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{createErr: errors.NewStd("rate limit exceeded")}
	p := &PersistentNotice{retryDelay: 20 * time.Millisecond}
	var mu sync.Mutex
	retried := false
	p.SetRetry(func() {
		mu.Lock()
		retried = true
		mu.Unlock()
	})

	require.Error(t, p.Reconcile(svc, want("a")))
	// The condition clears before the retry fires: the retry is cancelled.
	require.NoError(t, p.Reconcile(svc, want("")))
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	assert.False(t, retried)
	mu.Unlock()
}

func TestService_DeleteBroadcastsDeletion(t *testing.T) {
	t.Parallel()
	svc := NewService(DefaultServiceConfig())
	t.Cleanup(svc.Stop)

	notifCh, _ := svc.Subscribe()
	t.Cleanup(func() { svc.Unsubscribe(notifCh) })
	delCh, _ := svc.SubscribeDeletions()
	t.Cleanup(func() { svc.UnsubscribeDeletions(delCh) })

	n := NewNotification(TypeError, PriorityHigh, "t", "m")
	require.NoError(t, svc.CreateWithMetadata(n))
	<-notifCh // drain the create broadcast

	require.NoError(t, svc.Delete(n.ID))
	select {
	case ev := <-delCh:
		assert.Equal(t, n.ID, ev.ID)
		assert.Equal(t, TypeError, ev.Type)
	case <-time.After(time.Second):
		require.Fail(t, "no deletion event")
	}
	select {
	case got := <-notifCh:
		require.Failf(t, "deletion leaked to notification subscribers", "got %v", got)
	default:
	}

	// A missing ID is not an error and sends nothing.
	require.NoError(t, svc.Delete(n.ID))
	select {
	case ev := <-delCh:
		require.Failf(t, "deletion event for a missing notification", "got %v", ev)
	default:
	}
}

func TestService_UnsubscribeDeletions(t *testing.T) {
	t.Parallel()
	svc := NewService(DefaultServiceConfig())
	t.Cleanup(svc.Stop)

	delCh, ctx := svc.SubscribeDeletions()
	svc.UnsubscribeDeletions(delCh)
	require.Error(t, ctx.Err(), "unsubscribe cancels the subscription context")

	n := NewNotification(TypeInfo, PriorityLow, "t", "m")
	require.NoError(t, svc.CreateWithMetadata(n))
	require.NoError(t, svc.Delete(n.ID))
	select {
	case ev := <-delCh:
		require.Failf(t, "event after unsubscribe", "got %v", ev)
	default:
	}
}

// TestService_DeletionFanOutNeverBlocks pins the non-blocking send (a full
// subscriber channel drops the event) and the pruning of a subscriber whose
// context ended without an unsubscribe.
func TestService_DeletionFanOutNeverBlocks(t *testing.T) {
	t.Parallel()
	svc := NewService(DefaultServiceConfig())
	t.Cleanup(svc.Stop)

	full, _ := svc.SubscribeDeletions() // never drained
	for range DefaultChannelBufferSize + 1 {
		svc.broadcastDeletion(DeletedEvent{ID: "x"})
	}
	assert.Len(t, full, DefaultChannelBufferSize, "events beyond the buffer are dropped, not blocked on")

	_, ctx := svc.SubscribeDeletions()
	svc.deletionSubsMu.Lock()
	svc.deletionSubs[len(svc.deletionSubs)-1].cancel()
	svc.deletionSubsMu.Unlock()
	require.Error(t, ctx.Err())

	svc.broadcastDeletion(DeletedEvent{ID: "y"})
	svc.deletionSubsMu.Lock()
	n := len(svc.deletionSubs)
	svc.deletionSubsMu.Unlock()
	assert.Equal(t, 1, n, "the cancelled subscriber is pruned")
}
