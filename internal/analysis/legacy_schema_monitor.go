package analysis

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
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
)

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
// the legacy schema and keeps the migration_state telemetry tag current. It
// is only built for legacy installs, so v2 installs pay nothing.
type legacySchemaMonitor struct {
	states   migrationStateReader
	interval time.Duration
	log      logger.Logger

	// setTag publishes the tag value and reports whether it was applied (false
	// while telemetry is off); tests replace it.
	setTag func(string) bool

	// mu serializes evaluations and guards lastTag.
	mu      sync.Mutex
	lastTag string
	applied bool // lastTag was accepted by setTag; false means offer it again on the next tick, even when the read fails

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	started  atomic.Bool
}

// newLegacySchemaMonitor creates a monitor that polls states every interval.
func newLegacySchemaMonitor(states migrationStateReader, interval time.Duration, log logger.Logger) *legacySchemaMonitor {
	return &legacySchemaMonitor{
		states:   states,
		interval: interval,
		log:      log,
		setTag:   telemetry.SetMigrationStateTag,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// start evaluates once synchronously, then polls in a goroutine until stop.
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
	go m.run()
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
// database afterwards. It is idempotent and safe to call before start.
func (m *legacySchemaMonitor) stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	if m.started.Load() {
		<-m.doneCh
	}
}

// evaluate reads the state and publishes the tag when its value changed or the
// last publish was not applied (telemetry was off, for example). A failed read
// keeps the last value, and offers it again while it is not applied; before
// any successful read it publishes unavailable.
func (m *legacySchemaMonitor) evaluate() {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, err := m.states.GetState()
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
