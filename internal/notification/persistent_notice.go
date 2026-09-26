package notification

import (
	"sync"
	"time"
)

// ComponentClassifier is the notification component for notices raised by the
// acoustic classifier and the model gallery (model loading, optimize offers,
// inference failures, ONNX Runtime availability).
const ComponentClassifier = "classifier"

// Persistent-notice retry bounds. A create can fail transiently, most often on
// the shared notification rate limit (DefaultRateLimitMaxEvents per minute) at a
// busy startup, so a failed raise is retried a bounded number of times with a
// doubling delay whose first step is one rate-limit window (DefaultServiceConfig).
const (
	persistentNoticeRetryBaseDelay   = time.Minute
	persistentNoticeRetryMaxAttempts = 4
)

// NoticeService is the slice of the notification service a PersistentNotice
// uses. *Service implements it; tests pass a fake.
type NoticeService interface {
	CreateWithMetadata(notif *Notification) error
	Delete(id string) error
}

// PersistentNotice latches one persistent bell notice keyed by a signature: the
// notice is raised when a non-empty signature is first due, replaced when the
// signature changes (its text is stale), and deleted when the signature becomes
// empty. An unchanged signature is a no-op, so a notice the user deleted is not
// re-raised until the condition changes or the process restarts.
//
// A failed create is not latched and is retried by the retry callback (see
// SetRetry) up to persistentNoticeRetryMaxAttempts times with a doubling delay;
// any later trigger also retries. A failed delete keeps the latch, so the next
// trigger retries it instead of raising a second notice beside the old one.
//
// The zero value is ready to use. It is safe for concurrent use.
type PersistentNotice struct {
	mu  sync.Mutex
	id  string // live notification ID, "" when none is outstanding
	sig string // signature the live notice was raised for; set and cleared with id

	failedSig string // signature whose last apply failed
	attempts  int    // consecutive failed applies for failedSig

	retry      func()
	retryDelay time.Duration // base delay; zero means persistentNoticeRetryBaseDelay
	timer      *time.Timer
	stopped    bool
}

// SetRetry registers fn as the re-arm callback: after a failed apply it runs on
// a timer goroutine and is expected to trigger a fresh Reconcile (usually the
// owner's own sync function). It must not be called with the latch lock held,
// which the timer guarantees. A nil fn disables retries.
func (p *PersistentNotice) SetRetry(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retry = fn
}

// Reconcile computes the desired signature and applies it. compute runs under
// the latch lock, so concurrent reconciles cannot act on a condition that
// disagrees with the latch; it returns the signature ("" when no notice is due)
// and a builder for the notice, called only when a new notice must be created.
// It returns the delete or create error, if any.
func (p *PersistentNotice) Reconcile(svc NoticeService, compute func() (sig string, build func() *Notification)) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	sig, build := compute()
	if sig == p.sig {
		// The latched notice already matches (or none is due and none is out).
		p.resetRetryLocked()
		return nil
	}
	if p.id != "" {
		// A user-deleted notice is fine: Service.Delete treats a missing one as
		// success. Any other failure keeps the latch.
		if err := svc.Delete(p.id); err != nil {
			p.armRetryLocked(sig)
			return err
		}
		p.id = ""
		p.sig = ""
	}
	if sig == "" {
		p.resetRetryLocked()
		return nil
	}
	notif := build()
	if err := svc.CreateWithMetadata(notif); err != nil {
		p.armRetryLocked(sig)
		return err
	}
	p.id = notif.ID
	p.sig = sig
	p.resetRetryLocked()
	return nil
}

// ID returns the live notification ID, or "" when no notice is outstanding.
func (p *PersistentNotice) ID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.id
}

// Stop cancels a pending retry and disables later ones. The latched notice is
// left in place; clear it first with a Reconcile to "" if it must go.
func (p *PersistentNotice) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	p.stopTimerLocked()
}

// armRetryLocked records a failed apply of sig and schedules the retry callback
// unless the attempt budget for sig is spent. The caller holds p.mu.
func (p *PersistentNotice) armRetryLocked(sig string) {
	if sig != p.failedSig {
		p.failedSig = sig
		p.attempts = 0
	}
	p.attempts++
	p.stopTimerLocked()
	if p.retry == nil || p.stopped || p.attempts > persistentNoticeRetryMaxAttempts {
		return
	}
	base := p.retryDelay
	if base <= 0 {
		base = persistentNoticeRetryBaseDelay
	}
	p.timer = time.AfterFunc(base<<(p.attempts-1), p.retry)
}

// resetRetryLocked clears the failure bookkeeping after a successful apply. The
// caller holds p.mu.
func (p *PersistentNotice) resetRetryLocked() {
	p.failedSig = ""
	p.attempts = 0
	p.stopTimerLocked()
}

// stopTimerLocked cancels a pending retry. The caller holds p.mu.
func (p *PersistentNotice) stopTimerLocked() {
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
}
