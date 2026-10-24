package analysis

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

const (
	// legacySchemaPollInterval is how often the monitor re-reads the migration
	// state. Migration state changes are user-driven and slow, so a 30 second
	// lag is invisible, and one singleton-row read at that rate costs nothing.
	legacySchemaPollInterval = 30 * time.Second

	// migrationTagUnknown is the tag value for a state string outside the known set.
	migrationTagUnknown = "unknown"
	// migrationTagUnavailable is the tag value while the state cannot be read.
	migrationTagUnavailable = "unavailable"

	// legacySchemaWarnMessage is the startup WARN for an install that still
	// runs the legacy database schema.
	legacySchemaWarnMessage = "the legacy database schema is deprecated and will stop working in a future release; open System > Database in the web UI to run the migration"
	// legacySchemaCheckOperation is the operation field of the startup WARN.
	legacySchemaCheckOperation = "legacy_schema_check"
)

// legacyNoticeText is the notice for one legacy notice kind, used for the bell
// notice and the push reminder: its priority and its English fallback text,
// with the keys that let the UI translate it.
type legacyNoticeText struct {
	priority             notification.Priority
	title, message       string
	titleKey, messageKey string
}

// legacyNoticeKinds holds the notice text of every kind that raises a notice.
// LegacyNoticeNone has no entry.
var legacyNoticeKinds = map[migration.LegacyNoticeKind]legacyNoticeText{
	migration.LegacyNoticeNotStarted: {
		priority:   notification.PriorityMedium,
		title:      "Database upgrade required",
		message:    "This installation still uses the old database format, which will stop working in a future release. Open System > Database to run the migration.",
		titleKey:   notification.MsgMigrationLegacyNotStartedTitle,
		messageKey: notification.MsgMigrationLegacyNotStartedMessage,
	},
	migration.LegacyNoticeInProgress: {
		priority:   notification.PriorityMedium,
		title:      "Database migration in progress",
		message:    "The migration to the new database format is running. Keep BirdNET-Go running until it finishes; the old format will stop working in a future release.",
		titleKey:   notification.MsgMigrationLegacyInProgressTitle,
		messageKey: notification.MsgMigrationLegacyInProgressMessage,
	},
	migration.LegacyNoticeNeedsAttention: {
		priority:   notification.PriorityHigh,
		title:      "Database migration needs attention",
		message:    "The migration to the new database format is paused or has failed. Open System > Database to resume or retry it; the old format will stop working in a future release.",
		titleKey:   notification.MsgMigrationLegacyNeedsAttentionTitle,
		messageKey: notification.MsgMigrationLegacyNeedsAttentionMessage,
	},
	migration.LegacyNoticeRestartRequired: {
		priority:   notification.PriorityMedium,
		title:      "Restart to finish the database migration",
		message:    "The migration has finished. Restart BirdNET-Go to switch to the new database. If this message stays after a restart, check the log for database errors.",
		titleKey:   notification.MsgMigrationLegacyRestartRequiredTitle,
		messageKey: notification.MsgMigrationLegacyRestartRequiredMessage,
	},
}

// newLegacyNoticeNotification builds the notice from its text for one delivery
// target: the bell for the persistent notice, push for the reminder.
func newLegacyNoticeNotification(text *legacyNoticeText, target string) *notification.Notification {
	return notification.NewNotification(notification.TypeWarning, text.priority, text.title, text.message).
		WithComponent(notification.ComponentDatabase).
		WithTitleKey(text.titleKey, nil).
		WithMessageKey(text.messageKey, nil).
		WithDeliveryTarget(target)
}

// migrationStateReader reads the persisted migration state.
// *datastoreV2.StateManager satisfies it.
type migrationStateReader interface {
	GetState() (*entities.MigrationState, error)
}

// migrationStateTagValue maps a migration state read to the bounded value of
// the migration_state telemetry tag. The closed switch keeps the tag to the
// known statuses plus two fixed fallbacks; a raw value never passes through.
func migrationStateTagValue(state *entities.MigrationState, err error) string {
	if err != nil || state == nil {
		return migrationTagUnavailable
	}
	switch state.State {
	case entities.MigrationStatusIdle,
		entities.MigrationStatusInitializing,
		entities.MigrationStatusDualWrite,
		entities.MigrationStatusPaused,
		entities.MigrationStatusMigrating,
		entities.MigrationStatusValidating,
		entities.MigrationStatusCutover,
		entities.MigrationStatusCompleted,
		entities.MigrationStatusFailed:
		return string(state.State)
	default:
		return migrationTagUnknown
	}
}

// legacySchemaMonitor watches the migration state on installs that still run
// the legacy schema. It keeps the migration_state telemetry tag current and
// keeps one bell notice in line with the migration state and, when a reminder
// is attached, sends the weekly push reminder. It is only built for installs
// with a readable migration state, so v2 installs pay nothing.
type legacySchemaMonitor struct {
	states   migrationStateReader
	interval time.Duration
	log      logger.Logger

	// legacySchema reports that the process runs the legacy schema. Without it
	// no notice or startup WARN is raised.
	legacySchema bool

	// service returns the notification service, or nil while it is not up.
	// Tests replace it.
	service func() notification.NoticeService

	// setTag publishes the tag value and reports whether it was applied (false
	// while telemetry is off); tests replace it.
	setTag func(string) bool

	// mu serializes evaluations and guards lastTag, kind, latch and the
	// reminder. Lock order: mu, then the latch's own lock.
	mu      sync.Mutex
	lastTag string
	kind    migration.LegacyNoticeKind // last notice kind from a successful read; none before one
	latch   notification.PersistentNotice
	// reminder sends the weekly push reminder; nil when there is no store for it.
	reminder *legacyReminder
	applied  bool // lastTag was accepted by setTag; false means offer it again on the next tick, even when the read fails

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	started  atomic.Bool
}

// newLegacySchemaMonitor creates a monitor that polls states every interval.
// legacySchema is whether the process runs the legacy schema.
func newLegacySchemaMonitor(states migrationStateReader, interval time.Duration, log logger.Logger, legacySchema bool) *legacySchemaMonitor {
	return &legacySchemaMonitor{
		states:       states,
		interval:     interval,
		log:          log,
		legacySchema: legacySchema,
		kind:         migration.LegacyNoticeNone,
		service:      notification.DefaultNoticeService,
		setTag:       telemetry.SetMigrationStateTag,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// setReminder attaches the push reminder. Call it before start.
func (m *legacySchemaMonitor) setReminder(r *legacyReminder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reminder = r
}

// start evaluates once synchronously, logs the legacy schema startup WARN once
// when the process runs the legacy schema, then polls in a goroutine until stop.
// It does nothing when the monitor was already started or stopped. started is
// claimed before the stop check so a concurrent stop either sees it and waits
// for doneCh, or has closed stopCh before the check below.
func (m *legacySchemaMonitor) start() {
	if !m.started.CompareAndSwap(false, true) {
		return
	}
	select {
	case <-m.stopCh:
		close(m.doneCh) // a stop that saw started is waiting on it
		return
	default:
	}
	m.evaluate()
	m.warnLegacySchema()
	go m.run()
}

// warnLegacySchema logs the deprecation WARN once, after the first evaluation,
// when the process runs the legacy schema. The fields are bounded values; no
// path or error text is logged.
func (m *legacySchemaMonitor) warnLegacySchema() {
	if !m.legacySchema {
		return
	}
	m.mu.Lock()
	kind, tag := m.kind, m.lastTag
	m.mu.Unlock()
	logLegacySchemaWarning(m.log, kind, tag)
}

// logLegacySchemaWarning logs the legacy schema deprecation WARN. kind and tag
// are bounded values; no path or error text is logged.
func logLegacySchemaWarning(log logger.Logger, kind migration.LegacyNoticeKind, tag string) {
	log.Warn(legacySchemaWarnMessage,
		logger.String("notice_kind", string(kind)),
		logger.String("migration_state", tag),
		logger.String("operation", legacySchemaCheckOperation))
}

// run is the polling loop.
func (m *legacySchemaMonitor) run() {
	defer close(m.doneCh)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.evaluate()
		}
	}
}

// stop ends polling and waits for the goroutine, so nothing reads the
// database afterwards, then stops the notice latch so it raises nothing more.
// A notice already raised stays. It is idempotent and safe to call before start.
func (m *legacySchemaMonitor) stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	if m.started.Load() {
		<-m.doneCh
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latch.Stop()
}

// evaluate reads the state and publishes the tag when its value changed or the
// last publish was not applied (telemetry was off, for example). A failed read
// keeps the last value, and offers it again while it is not applied; before
// any successful read it publishes unavailable. It then brings the bell notice
// in line with the notice kind of the last successful read and offers that kind
// to the push reminder, on every return path.
func (m *legacySchemaMonitor) evaluate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Deferred first so it runs after reconcileLocked and on every return path.
	defer m.remindLocked()
	defer m.reconcileLocked()

	state, err := m.states.GetState()
	if err == nil && state != nil {
		m.kind = migration.LegacyNotice(m.legacySchema, state.State)
	}
	if err != nil {
		m.log.Debug("failed to read migration state for telemetry tag",
			logger.Error(err),
			logger.String("operation", "legacy_schema_monitor_evaluate"))
		if m.lastTag != "" {
			if !m.applied {
				m.applied = m.setTag(m.lastTag)
			}
			return
		}
	}

	tag := migrationStateTagValue(state, err)
	if tag == m.lastTag && m.applied {
		return
	}
	m.lastTag = tag
	m.applied = m.setTag(tag)
}

// reconcileLocked applies the current notice kind to the latch. The caller
// holds m.mu. Without a notification service nothing is applied; the next tick
// is the retry, as it is after a failed create.
func (m *legacySchemaMonitor) reconcileLocked() {
	svc := m.service()
	if svc == nil {
		return
	}
	kind := m.kind
	if err := m.latch.Reconcile(svc, func() (string, func() *notification.Notification) {
		text, ok := legacyNoticeKinds[kind]
		if !ok {
			return "", nil
		}
		return string(kind), func() *notification.Notification {
			return newLegacyNoticeNotification(&text, notification.DeliveryTargetBell)
		}
	}); err != nil {
		m.log.Warn("failed to update legacy database schema notification",
			logger.Error(err),
			logger.String("operation", legacySchemaCheckOperation))
	}
}

// remindLocked offers the current notice kind to the push reminder. The caller
// holds m.mu. Without a notification service nothing is sent or recorded; the
// next tick is the retry.
func (m *legacySchemaMonitor) remindLocked() {
	if m.reminder == nil {
		return
	}
	svc := m.service()
	if svc == nil {
		return
	}
	m.reminder.check(m.kind, svc)
}
