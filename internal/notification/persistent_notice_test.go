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
	// The first failure plus persistentNoticeMaxRetries retries, then the
	// budget is spent and no further timer is armed.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return retries == persistentNoticeMaxRetries
	}, 5*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	assert.Equal(t, persistentNoticeMaxRetries, retries, "retries are bounded")
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
	// An hour-long delay never fires during the test, so the assertions read
	// the armed timer directly instead of racing a real one.
	p := &PersistentNotice{retryDelay: time.Hour}
	p.SetRetry(func() {})

	require.Error(t, p.Reconcile(svc, want("a")))
	require.True(t, retryArmed(p), "a failed create arms a retry")

	p.Stop()
	assert.False(t, retryArmed(p), "Stop cancels the pending retry")

	require.NoError(t, p.Reconcile(svc, want("a")), "a stopped latch does not try to raise")
	assert.False(t, retryArmed(p), "and arms no retry")
}

func TestPersistentNotice_RecoveryCancelsRetry(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{createErr: errors.NewStd("rate limit exceeded")}
	p := &PersistentNotice{retryDelay: time.Hour}
	p.SetRetry(func() {})

	require.Error(t, p.Reconcile(svc, want("a")))
	require.True(t, retryArmed(p))
	// The condition clears before the retry fires: the retry is cancelled.
	require.NoError(t, p.Reconcile(svc, want("")))
	assert.False(t, retryArmed(p))
}

// TestPersistentNotice_ReplaceWithFailingDeleteKeepsOldNotice pins the replace
// step's first-step failure: the old notice stays latched, and a later apply of
// the new signature deletes it before raising the replacement.
func TestPersistentNotice_ReplaceWithFailingDeleteKeepsOldNotice(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{}
	p := &PersistentNotice{retryDelay: time.Hour}
	require.NoError(t, p.Reconcile(svc, want("a")))
	old := p.ID()

	svc.deleteErr = errors.NewStd("store down")
	require.Error(t, p.Reconcile(svc, want("b")))
	assert.Equal(t, old, p.ID(), "the old notice is still latched")
	created, _ := svc.counts()
	assert.Equal(t, 1, created, "no replacement beside the undeleted notice")

	svc.deleteErr = nil
	require.NoError(t, p.Reconcile(svc, want("b")))
	require.Equal(t, []string{old}, svc.deleted)
	assert.NotEqual(t, old, p.ID())
}

// retryArmed reports whether p has a pending retry timer.
func retryArmed(p *PersistentNotice) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.timer != nil
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

// TestPersistentNotice_StoppedRefusesRaiseButClears pins that a stopped latch
// raises no new notice (its owner is torn down) while it can still clear one.
func TestPersistentNotice_StoppedRefusesRaiseButClears(t *testing.T) {
	t.Parallel()
	svc := &fakeNoticeService{}
	var p PersistentNotice
	require.NoError(t, p.Reconcile(svc, want("a")))
	require.NotEmpty(t, p.ID())

	p.Stop()
	require.NoError(t, p.Reconcile(svc, want("b")))
	created, deleted := svc.counts()
	assert.Equal(t, 1, created, "a stopped latch raises no replacement")
	assert.Equal(t, 1, deleted, "but the stale notice is still deleted")
	assert.Empty(t, p.ID())

	require.NoError(t, p.Reconcile(svc, want("c")))
	created, _ = svc.counts()
	assert.Equal(t, 1, created, "nor a fresh notice")
}

// TestPersistentNotice_RejectsUnidentifiableNotice pins that a missing builder,
// a nil notice or an ID-less notice is an error and latches nothing (an ID-less
// notice could never be deleted).
func TestPersistentNotice_RejectsUnidentifiableNotice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		build func() *Notification
	}{
		{name: "nil builder", build: nil},
		{name: "nil notice", build: func() *Notification { return nil }},
		{name: "empty id", build: func() *Notification { return &Notification{Title: "t"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := &fakeNoticeService{}
			var p PersistentNotice
			err := p.Reconcile(svc, func() (string, func() *Notification) { return "a", tt.build })
			require.Error(t, err)
			assert.Empty(t, p.ID())
			created, _ := svc.counts()
			assert.Zero(t, created)
		})
	}
}

// TestService_StopDropsDeletionSubscribers pins that Stop cancels and drops the
// deletion registrations along with the notification subscribers.
func TestService_StopDropsDeletionSubscribers(t *testing.T) {
	t.Parallel()
	svc := NewService(DefaultServiceConfig())
	_, ctx := svc.SubscribeDeletions()
	svc.Stop()

	require.Error(t, ctx.Err())
	svc.deletionSubsMu.Lock()
	defer svc.deletionSubsMu.Unlock()
	assert.Nil(t, svc.deletionSubs)
}
