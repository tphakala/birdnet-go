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
	"github.com/tphakala/birdnet-go/internal/health/checks"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// storageNoticeDebounce coalesces a burst of triggers into one evaluation.
const storageNoticeDebounce = 3 * time.Second

// Separators of the notice signature and of the path list in the notice text.
const (
	storageSignatureSeparator = "|"
	storagePathSeparator      = ", "
)

// storageNotice holds the storage notice state of a Handler: one latch per
// notice kind, keyed by a signature of the paths involved, and the debounce
// timer. The zero value is ready to use.
type storageNotice struct {
	// mu serializes whole evaluations (compute and apply), so two overlapping
	// syncs cannot apply their results out of order.
	mu sync.Mutex
	// lost is the notice for paths lost when the container is recreated, and
	// anonymous the one for paths in anonymous volumes.
	lost      notification.PersistentNotice
	anonymous notification.PersistentNotice

	// timerMu guards the debounce timer and the started and stopped flags.
	timerMu sync.Mutex
	timer   *time.Timer
	started bool
	stopped bool

	// service and report are test seams; nil means the notification service
	// resolved by noticeSvc and Handler.storagePersistenceReport.
	service func() notification.NoticeService
	report  func() checks.StoragePersistenceReport
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

// noticeSvc resolves the notification service: a test fake, the injected one, or
// else the process-wide service. It returns nil when none exists yet.
func (c *Handler) noticeSvc() notification.NoticeService {
	if c.storage.service != nil {
		return c.storage.service()
	}
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

// newStorageLostNotification builds the bell notice for paths lost when the
// container is recreated. It is high priority when the config or database is
// among them and medium otherwise.
func newStorageLostNotification(vs []diagnostics.StorageVerdict) *notification.Notification {
	priority := notification.PriorityMedium
	for i := range vs {
		if vs[i].Severity() == diagnostics.StorageSeverityCritical {
			priority = notification.PriorityHigh
			break
		}
	}
	paths := storagePathList(vs)
	return notification.NewNotification(
		notification.TypeWarning,
		priority,
		"Data will be lost when the container is recreated",
		"These paths are not on a mounted volume, so their files are deleted when the container is recreated, for example on an image update: "+paths+". Map a volume or a host directory to them in your container configuration.",
	).
		WithComponent(notification.ComponentStorage).
		WithTitleKey(notification.MsgStorageNotPersistentTitle, nil).
		WithMessageKey(notification.MsgStorageNotPersistentMessage, map[string]any{"paths": paths}).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// newStorageAnonymousNotification builds the low priority bell notice for paths
// in anonymous volumes.
func newStorageAnonymousNotification(vs []diagnostics.StorageVerdict) *notification.Notification {
	paths := storagePathList(vs)
	return notification.NewNotification(
		notification.TypeWarning,
		notification.PriorityLow,
		"Data is stored in an anonymous container volume",
		"These paths are in anonymous volumes that the container runtime created because no volume was mapped: "+paths+". A container started fresh, for example with docker run after an image update, gets new empty volumes, so the data seems lost. Map a named volume or a host directory to them.",
	).
		WithComponent(notification.ComponentStorage).
		WithTitleKey(notification.MsgStorageAnonymousVolumeTitle, nil).
		WithMessageKey(notification.MsgStorageAnonymousVolumeMessage, map[string]any{"paths": paths}).
		WithDeliveryTarget(notification.DeliveryTargetBell)
}

// storageReport returns the current persistence report.
func (c *Handler) storageReport() checks.StoragePersistenceReport {
	if c.storage.report != nil {
		return c.storage.report()
	}
	return c.storagePersistenceReport()
}

// syncStorageNotices evaluates the key paths and raises, replaces or clears the
// two persistent bell notices to match. It is idempotent and safe to call
// concurrently. An unreadable mount table, or a path that cannot be judged while a
// notice has nothing else to name, leaves that notice as it is: an unknown
// result is neither an alarm nor proof that the problem went away.
func (c *Handler) syncStorageNotices() {
	svc := c.noticeSvc()
	if svc == nil {
		return
	}
	n := &c.storage
	n.mu.Lock()
	defer n.mu.Unlock()

	report := c.storageReport()
	if report.Applicable && report.Err != nil {
		c.LogWarnIfEnabled("storage notice skipped: mount table unreadable", logger.Error(report.Err))
		return
	}

	var lost, anonymous []diagnostics.StorageVerdict
	unjudged := false
	if report.Applicable {
		for i := range report.Verdicts {
			switch v := report.Verdicts[i]; {
			case v.Class == diagnostics.PersistenceUnknown:
				unjudged = true
			case v.Lost():
				lost = append(lost, v)
			case v.Class == diagnostics.PersistenceAnonymousVolume:
				anonymous = append(anonymous, v)
			}
		}
	}

	// A path that could not be judged may be one of the paths a notice names, so
	// an empty set is not proof that the problem went away: leave that notice as
	// it is until every path is judged.
	if len(lost) > 0 || !unjudged {
		c.reconcileStorageNotice(svc, &n.lost, lost, newStorageLostNotification)
	}
	if len(anonymous) > 0 || !unjudged {
		c.reconcileStorageNotice(svc, &n.anonymous, anonymous, newStorageAnonymousNotification)
	}
}

// reconcileStorageNotice applies one verdict set to its latch. A failure is
// logged and re-arms a bounded retry through the latch; it never blocks the
// other notice.
func (c *Handler) reconcileStorageNotice(svc notification.NoticeService, latch *notification.PersistentNotice, vs []diagnostics.StorageVerdict, build func([]diagnostics.StorageVerdict) *notification.Notification) {
	latch.SetRetry(c.ScheduleStorageNoticeSync)
	err := latch.Reconcile(svc, func() (string, func() *notification.Notification) {
		sig := storageSignature(vs)
		if sig == "" {
			return "", nil
		}
		return sig, func() *notification.Notification { return build(vs) }
	})
	if err != nil {
		c.LogWarnIfEnabled("failed to update storage notification",
			logger.Int("path_count", len(vs)), logger.Error(err))
	}
}

// StartStorageNoticeSync enables storage notice evaluations and schedules the
// first one (the startup evaluation). Until it is called,
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
	c.armStorageNoticeTimerLocked()
}

// ScheduleStorageNoticeSync re-evaluates the storage notices after a short
// debounce. It is a no-op before StartStorageNoticeSync and after
// StopStorageNoticeSync.
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
	c.armStorageNoticeTimerLocked()
}

// armStorageNoticeTimerLocked (re)starts the debounce timer. The caller holds
// c.storage.timerMu.
func (c *Handler) armStorageNoticeTimerLocked() {
	n := &c.storage
	if n.timer != nil {
		n.timer.Stop()
	}
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
	n.lost.Stop()
	n.anonymous.Stop()
}
