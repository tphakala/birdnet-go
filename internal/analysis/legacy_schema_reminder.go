package analysis

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

const (
	// legacyReminderInterval is the minimum time between two push reminders.
	legacyReminderInterval = 7 * 24 * time.Hour
	// legacyReminderStartupDelay is how long after the first check the reminder
	// stays quiet, so a restart loop or a short-lived run never pushes.
	legacyReminderStartupDelay = 10 * time.Minute
	// legacyReminderMetadataKey is the v2 app_metadata key holding the time of
	// the last reminder, as an RFC 3339 UTC string.
	legacyReminderMetadataKey = "legacy_migration_reminder_last_sent"
	// legacyReminderStoreTimeout bounds each read or write of the stored time.
	legacyReminderStoreTimeout = 5 * time.Second
	// legacyReminderOperation is the operation field of the reminder's log lines.
	legacyReminderOperation = "legacy_schema_reminder"
)

// appMetadataStore is the slice of the v2 app_metadata repository the reminder
// uses. A missing key reads as an empty string without an error.
type appMetadataStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// legacyReminder sends the weekly push reminder for installs that still run the
// legacy database schema. It is driven by the legacy schema monitor, under the
// monitor's lock, so it needs no locking of its own.
type legacyReminder struct {
	store    appMetadataStore
	settings func() *conf.Settings
	log      logger.Logger

	// now returns the current time; tests replace it.
	now func() time.Time

	// pollInterval is the monitor's poll interval. Push must have looked enabled
	// on two checks at least this far apart before a send, so a send cannot land
	// before a dispatcher rebuilt after a settings save has subscribed.
	pollInterval time.Duration

	// buildDate is the binary's build time; zero when unknown. A clock behind it
	// is not trusted.
	buildDate time.Time

	startedAt   time.Time // time of the first check; zero before it
	pushOnSince time.Time // first check that saw push on; zero while push is off
	lastSent    time.Time // time of the last reminder; zero when none is known
	loaded      bool      // lastSent was read from the store
}

// newLegacyReminder creates a reminder that persists its last-sent time in
// store. buildDate is the RFC 3339 build date; one that does not parse disables
// the clock guard. settings is read on every check so changes apply at once.
func newLegacyReminder(store appMetadataStore, settings func() *conf.Settings, buildDate string, pollInterval time.Duration, log logger.Logger) *legacyReminder {
	r := &legacyReminder{
		store:        store,
		settings:     settings,
		log:          log,
		now:          time.Now,
		pollInterval: pollInterval,
	}
	if built, err := time.Parse(time.RFC3339, buildDate); err == nil {
		r.buildDate = built
	}
	return r
}

// legacyReminderKind reports whether a notice kind gets a push reminder. Only
// the kinds that ask the user to act do; a running migration needs nothing and
// a finished one has already been announced.
func legacyReminderKind(kind migration.LegacyNoticeKind) bool {
	return kind == migration.LegacyNoticeNotStarted || kind == migration.LegacyNoticeNeedsAttention
}

// pushEnabled reports whether push notifications can currently reach anyone:
// push is on and at least one provider is enabled.
func (r *legacyReminder) pushEnabled() bool {
	s := r.settings()
	if s == nil || !s.Notification.Push.Enabled {
		return false
	}
	for i := range s.Notification.Push.Providers {
		if s.Notification.Push.Providers[i].Enabled {
			return true
		}
	}
	return false
}

// check sends a reminder through svc when one is due for kind. It is called on
// every monitor tick.
func (r *legacyReminder) check(kind migration.LegacyNoticeKind, svc notification.NoticeService) {
	now := r.now()
	if r.startedAt.IsZero() {
		r.startedAt = now
	}
	switch {
	case !r.pushEnabled():
		r.pushOnSince = time.Time{}
	case r.pushOnSince.IsZero():
		r.pushOnSince = now
	}

	if svc == nil || !legacyReminderKind(kind) ||
		now.Sub(r.startedAt) < legacyReminderStartupDelay ||
		(!r.buildDate.IsZero() && now.Before(r.buildDate)) ||
		r.pushOnSince.IsZero() || now.Sub(r.pushOnSince) < r.pollInterval {
		return
	}
	if !r.load() || !r.due(now) {
		return
	}
	r.send(kind, now, svc)
}

// load reads the stored last-sent time once. It reports false while the store
// cannot be read, so nothing is sent on an unknown last-sent time; the next
// check retries. A missing or unparsable value counts as never sent.
func (r *legacyReminder) load() bool {
	if r.loaded {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), legacyReminderStoreTimeout)
	defer cancel()
	value, err := r.store.Get(ctx, legacyReminderMetadataKey)
	if err != nil {
		r.log.Warn("failed to read legacy database reminder time",
			logger.Error(err),
			logger.String("operation", legacyReminderOperation))
		return false
	}
	if stored, perr := time.Parse(time.RFC3339, value); perr == nil {
		r.lastSent = stored
	}
	r.loaded = true
	return true
}

// due reports whether a reminder is owed at now. A stored time in the future,
// left by a clock that ran ahead, gives a negative elapsed time and is never due.
func (r *legacyReminder) due(now time.Time) bool {
	return r.lastSent.IsZero() || now.Sub(r.lastSent) >= legacyReminderInterval
}

// send creates the push notification and records it as sent. A failed create
// records nothing, so the next tick retries. The in-memory time is set before
// the write, so a failed write cannot cause a second send in this run.
func (r *legacyReminder) send(kind migration.LegacyNoticeKind, now time.Time, svc notification.NoticeService) {
	text, ok := legacyNoticeKinds[kind]
	if !ok {
		return
	}
	if err := svc.CreateWithMetadata(newLegacyNoticeNotification(&text, notification.DeliveryTargetPush)); err != nil {
		r.log.Warn("failed to send legacy database reminder",
			logger.Error(err),
			logger.String("operation", legacyReminderOperation))
		return
	}
	r.lastSent = now

	ctx, cancel := context.WithTimeout(context.Background(), legacyReminderStoreTimeout)
	defer cancel()
	if err := r.store.Set(ctx, legacyReminderMetadataKey, now.UTC().Format(time.RFC3339)); err != nil {
		r.log.Warn("failed to store legacy database reminder time",
			logger.Error(err),
			logger.String("operation", legacyReminderOperation))
	}
}
