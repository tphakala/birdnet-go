package system

// storage_notice.go raises persistent bell notices when key storage paths would
// lose data: paths on a container's own layer or a memory filesystem are
// deleted when the container is recreated (for example by an image update), and
// paths in anonymous volumes seem lost after a fresh container start. The
// evaluation is shared with the storage_persistence health check
// (storage_persistence.go). It runs at startup and again, debounced, when a
// settings save changes one of the paths.

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// storageNoticeDebounce coalesces a burst of triggers into one evaluation.
const storageNoticeDebounce = 3 * time.Second

// storageNoticeNoServiceMaxRetries bounds how often an evaluation that found no
// notification service re-arms itself. The retries wait storageNoticeDebounce,
// then double each time, so a process without notifications stops after a few
// timers and a late service is still found by a later settings save.
const storageNoticeNoServiceMaxRetries = 4

// Separators of the notice signature and of the path list in the notice text,
// and the placeholder the English fallback text uses for the path list.
const (
	storageSignatureSeparator = "|"
	storagePathSeparator      = ", "
	storagePathsPlaceholder   = "{paths}"
)

// storageNoticeKind describes one of the two storage notices: which verdicts it
// names, its priority and its text.
type storageNoticeKind struct {
	// include selects the verdicts the notice names.
	include func(*diagnostics.StorageVerdict) bool
	// priority is the notice priority, raised to high when escalate is set and a
	// named verdict is critical.
	priority notification.Priority
	escalate bool
	// title and message are the English fallback text; message holds the path
	// list placeholder. The keys let the UI translate them.
	title, message       string
	titleKey, messageKey string
}

// storageNoticeKinds are the lost notice and the anonymous volume notice. Their
// latches are storageNotice.latches at the same index.
var storageNoticeKinds = [...]storageNoticeKind{
	{
		include:  func(v *diagnostics.StorageVerdict) bool { return v.Lost() },
		priority: notification.PriorityMedium,
		escalate: true,
		title:    "Data will be lost when the container is recreated",
		message:  "These paths are on storage that is deleted when the container is recreated, for example on an image update: " + storagePathsPlaceholder + ". Map a volume or a host directory to them in your container configuration.",
		titleKey: notification.MsgStorageNotPersistentTitle, messageKey: notification.MsgStorageNotPersistentMessage,
	},
	{
		include:  func(v *diagnostics.StorageVerdict) bool { return v.Class == diagnostics.PersistenceAnonymousVolume },
		priority: notification.PriorityLow,
		title:    "Data is stored in an anonymous container volume",
		message:  "These paths are in anonymous volumes that the container runtime created because no volume was mapped: " + storagePathsPlaceholder + ". A container started fresh, for example with docker run after an image update, gets new empty volumes, so the data seems lost. Map a named volume or a host directory to them.",
		titleKey: notification.MsgStorageAnonymousVolumeTitle, messageKey: notification.MsgStorageAnonymousVolumeMessage,
	},
}

// storageNotice holds the storage notice state of a Handler: one latch per
// notice kind, keyed by a signature of the paths involved, the last known
// verdict per label, and the debounce timer. The zero value is ready to use.
type storageNotice struct {
	// mu serializes whole evaluations (compute and apply), so two overlapping
	// syncs cannot apply their results out of order. It guards known.
	mu sync.Mutex
	// latches holds one latch per storageNoticeKinds entry.
	latches [len(storageNoticeKinds)]notification.PersistentNotice
	// known is the last judged verdict per label. A path that cannot be judged
	// in a later round keeps its last verdict, so it neither shrinks a live
	// notice nor brings back one the user deleted.
	known map[string]diagnostics.StorageVerdict

	// timerMu guards the debounce timer, the arm count and the started and
	// stopped flags.
	timerMu sync.Mutex
	timer   *time.Timer
	arms    int
	// noServiceRetries counts the retries armed since the last evaluation that
	// found a notification service or the last external schedule.
	noServiceRetries int
	started          bool
	stopped          bool
}

// StorageNoticeSyncStatus is a snapshot of the debounce timer, for the facade's
// wiring tests.
type StorageNoticeSyncStatus struct {
	// Arms counts how often a start or a schedule armed the debounce timer.
	// The retries made while no notification service exists are not counted.
	Arms int
	// Stopped reports that StopStorageNoticeSync has run.
	Stopped bool
}

// StorageNoticeSyncStatus returns the debounce timer snapshot.
func (c *Handler) StorageNoticeSyncStatus() StorageNoticeSyncStatus {
	n := &c.storage
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	return StorageNoticeSyncStatus{Arms: n.arms, Stopped: n.stopped}
}

// SetNotificationService routes the storage notices to svc, the notification
// service the facade injects, instead of the process-wide one. A nil svc keeps
// the process-wide fallback. Call it before StartStorageNoticeSync.
func (c *Handler) SetNotificationService(svc *notification.Service) {
	if c == nil || svc == nil {
		return
	}
	c.notices = svc
}

// noticeSvc resolves the notification service: the injected one, else the
// process-wide service. It returns nil when none exists yet.
func (c *Handler) noticeSvc() notification.NoticeService {
	if c.notices != nil {
		return c.notices
	}
	return notification.DefaultNoticeService()
}

// storageSignature identifies a verdict set independent of order and of the
// other paths: the sorted label=resolvedPath entries. An empty set gives "".
func storageSignature(vs []diagnostics.StorageVerdict) string {
	parts := make([]string, len(vs))
	for i := range vs {
		parts[i] = vs[i].Label + "=" + vs[i].ResolvedPath
	}
	slices.Sort(parts)
	return strings.Join(parts, storageSignatureSeparator)
}

// storagePathList joins the distinct resolved paths in verdict order.
func storagePathList(vs []diagnostics.StorageVerdict) string {
	paths := make([]string, 0, len(vs))
	for i := range vs {
		if !slices.Contains(paths, vs[i].ResolvedPath) {
			paths = append(paths, vs[i].ResolvedPath)
		}
	}
	return strings.Join(paths, storagePathSeparator)
}

// newNotification builds the bell notice of this kind for a non-empty verdict
// set.
func (k *storageNoticeKind) newNotification(vs []diagnostics.StorageVerdict) *notification.Notification {
	priority := k.priority
	if k.escalate {
		for i := range vs {
			if vs[i].Severity() == diagnostics.StorageSeverityCritical {
				priority = notification.PriorityHigh
				break
			}
		}
	}
	paths := storagePathList(vs)
	return notification.NewNotification(
		notification.TypeWarning,
		priority,
		k.title,
		strings.ReplaceAll(k.message, storagePathsPlaceholder, paths),
	).
		WithComponent(notification.ComponentStorage).
		WithTitleKey(k.titleKey, nil).
		WithMessageKey(k.messageKey, map[string]any{"paths": paths}).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// effectiveVerdicts returns the verdicts the notices are computed from and
// updates the last known verdict per label. A path that cannot be judged keeps
// its last verdict when its configured path is unchanged, and is left out when
// it has none. A label the report no longer lists is forgotten. The caller holds
// n.mu.
func (n *storageNotice) effectiveVerdicts(report []diagnostics.StorageVerdict) []diagnostics.StorageVerdict {
	next := make(map[string]diagnostics.StorageVerdict, len(report))
	effective := make([]diagnostics.StorageVerdict, 0, len(report))
	for i := range report {
		v := report[i]
		if v.Class == diagnostics.PersistenceUnknown {
			prior, ok := n.known[v.Label]
			if !ok || prior.Path != v.Path {
				continue
			}
			v = prior
		}
		next[v.Label] = v
		effective = append(effective, v)
	}
	n.known = next
	return effective
}

// syncStorageNotices evaluates the key paths and raises, replaces or clears the
// two persistent bell notices to match. It is idempotent and safe to call
// concurrently. An unreadable mount table or a missing settings snapshot leaves
// both notices as they are, and a path that cannot be judged keeps its last
// known verdict: an unknown result is neither an alarm nor proof that the
// problem went away.
// Without a notification service it re-arms itself a bounded number of times
// (retryStorageNoticeWithoutService).
func (c *Handler) syncStorageNotices() {
	svc := c.noticeSvc()
	if svc == nil {
		c.retryStorageNoticeWithoutService()
		return
	}
	c.resetStorageNoticeRetries()
	n := &c.storage
	n.mu.Lock()
	defer n.mu.Unlock()

	report := c.storagePersistenceReport()
	if report.Applicable && report.Err != nil {
		c.LogWarnIfEnabled("storage notice skipped: storage state unreadable", logger.Error(report.Err))
		return
	}

	var effective []diagnostics.StorageVerdict
	if report.Applicable {
		effective = n.effectiveVerdicts(report.Verdicts)
	} else {
		n.known = nil
	}

	for i := range storageNoticeKinds {
		kind := &storageNoticeKinds[i]
		var named []diagnostics.StorageVerdict
		for j := range effective {
			if kind.include(&effective[j]) {
				named = append(named, effective[j])
			}
		}
		err := n.latches[i].Reconcile(svc, func() (string, func() *notification.Notification) {
			sig := storageSignature(named)
			if sig == "" {
				return "", nil
			}
			return sig, func() *notification.Notification { return kind.newNotification(named) }
		})
		if err != nil {
			c.LogWarnIfEnabled("failed to update storage notification",
				logger.Int("path_count", len(named)), logger.Error(err))
		}
	}
}

// retryStorageNoticeWithoutService re-arms the evaluation after the debounce,
// doubled for each earlier retry, because no notification service exists yet. It
// arms nothing before Start, after Stop, or once storageNoticeNoServiceMaxRetries
// retries were armed.
func (c *Handler) retryStorageNoticeWithoutService() {
	n := &c.storage
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	if !n.started || n.stopped || n.noServiceRetries >= storageNoticeNoServiceMaxRetries {
		return
	}
	delay := storageNoticeDebounce << n.noServiceRetries
	n.noServiceRetries++
	if n.timer != nil {
		n.timer.Stop()
	}
	n.timer = time.AfterFunc(delay, c.fireStorageNoticeSync)
}

// resetStorageNoticeRetries starts the retry budget over.
func (c *Handler) resetStorageNoticeRetries() {
	n := &c.storage
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	n.noServiceRetries = 0
}

// StartStorageNoticeSync enables storage notice evaluations, lets the notice
// latches retry a failed update through ScheduleStorageNoticeSync, and schedules
// the first evaluation (the startup evaluation). Until it is called,
// ScheduleStorageNoticeSync is a no-op, so a handler that is not serving never
// arms a timer. It is a no-op after StopStorageNoticeSync.
func (c *Handler) StartStorageNoticeSync() {
	if c == nil {
		return
	}
	n := &c.storage
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	if n.stopped {
		return
	}
	n.started = true
	for i := range n.latches {
		n.latches[i].SetRetry(c.ScheduleStorageNoticeSync)
	}
	c.armStorageNoticeTimerLocked()
}

// ScheduleStorageNoticeSync re-evaluates the storage notices after a short
// debounce, and starts the retry budget for a missing notification service over.
// It is a no-op before StartStorageNoticeSync and after StopStorageNoticeSync.
func (c *Handler) ScheduleStorageNoticeSync() {
	if c == nil {
		return
	}
	n := &c.storage
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	if !n.started || n.stopped {
		return
	}
	n.noServiceRetries = 0
	c.armStorageNoticeTimerLocked()
}

// armStorageNoticeTimerLocked (re)starts the debounce timer. The caller holds
// c.storage.timerMu.
func (c *Handler) armStorageNoticeTimerLocked() {
	n := &c.storage
	if n.timer != nil {
		n.timer.Stop()
	}
	n.arms++
	n.timer = time.AfterFunc(storageNoticeDebounce, c.fireStorageNoticeSync)
}

// fireStorageNoticeSync is the debounce timer's callback. It runs the evaluation
// as a Core-tracked goroutine so Core.Wait joins it, and starts nothing once
// StopStorageNoticeSync has run: the stopped check and the tracked start happen
// under timerMu, which Stop takes to set stopped.
func (c *Handler) fireStorageNoticeSync() {
	n := &c.storage
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	if n.stopped {
		return
	}
	c.Go(c.syncStorageNotices)
}

// StopStorageNoticeSync stops any pending evaluation, ignores later schedules,
// and cancels pending retries of failed notice updates. The latched notices
// themselves are left in place. Controller.Shutdown calls it before Core.Cancel
// and Core.Wait.
func (c *Handler) StopStorageNoticeSync() {
	if c == nil {
		return
	}
	n := &c.storage
	n.timerMu.Lock()
	defer n.timerMu.Unlock()
	n.stopped = true
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
	for i := range n.latches {
		n.latches[i].Stop()
	}
}
