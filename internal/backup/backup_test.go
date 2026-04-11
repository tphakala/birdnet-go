package backup

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestStart_NoSourcesOrTargets_NoError verifies that calling Start on a
// backup manager that is enabled but has no sources or targets registered
// returns nil without emitting a telemetry event. This prevents Sentry
// noise from users who toggled backup on but never finished configuring it.
func TestStart_NoSourcesOrTargets_NoError(t *testing.T) {
	// Install a hook that records any error reported while the test runs.
	// Not parallel - the errors package keeps the hook list as package state.
	var (
		mu          sync.Mutex
		capturedErr []*errors.EnhancedError
	)
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		mu.Lock()
		defer mu.Unlock()
		capturedErr = append(capturedErr, ee)
	})
	t.Cleanup(errors.ClearErrorHooks)

	cfg := &conf.Settings{}
	cfg.Backup.Enabled = true

	m := &Manager{
		config:     &cfg.Backup,
		fullConfig: cfg,
		sources:    make(map[string]Source),
		targets:    make(map[string]Target),
		logger:     GetLogger().Module("manager-test"),
	}

	require.NoError(t, m.Start(), "Start should succeed when backup is enabled but no sources/targets are configured")

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, capturedErr, "no telemetry event should be emitted for an unconfigured but enabled backup manager")
}

// TestStart_MissingTarget_StillErrors ensures that the half-configured
// case (sources registered, no targets) continues to fail fast so users
// get feedback when they only partially set up backup.
func TestStart_MissingTarget_StillErrors(t *testing.T) {
	cfg := &conf.Settings{}
	cfg.Backup.Enabled = true

	m := &Manager{
		config:     &cfg.Backup,
		fullConfig: cfg,
		sources: map[string]Source{
			"dummy": dummySource{},
		},
		targets: make(map[string]Target),
		logger:  GetLogger().Module("manager-test"),
	}

	err := m.Start()
	require.Error(t, err, "Start should fail when sources are registered but targets are missing")
	assert.Contains(t, err.Error(), "no backup targets registered")
}

// TestRunBackup_NoSourcesOrTargets_NoError verifies that scheduler-driven
// RunBackup invocations on a manager with no registered sources or targets
// return nil without emitting telemetry. This mirrors the Start() semantics
// added alongside this fix and prevents Sentry noise from cron-triggered
// runs hitting users who enabled backup but never finished configuring it.
func TestRunBackup_NoSourcesOrTargets_NoError(t *testing.T) {
	// Not parallel: the errors package keeps the hook list as package state.
	var (
		mu          sync.Mutex
		capturedErr []*errors.EnhancedError
	)
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		mu.Lock()
		defer mu.Unlock()
		capturedErr = append(capturedErr, ee)
	})
	t.Cleanup(errors.ClearErrorHooks)

	cfg := &conf.Settings{}
	cfg.Backup.Enabled = true

	m := &Manager{
		config:     &cfg.Backup,
		fullConfig: cfg,
		sources:    make(map[string]Source),
		targets:    make(map[string]Target),
		logger:     GetLogger().Module("manager-test"),
	}

	require.NoError(t, m.RunBackup(t.Context()),
		"RunBackup should succeed when backup is enabled but no sources/targets are configured")

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, capturedErr,
		"no telemetry event should be emitted for an unconfigured but enabled backup manager")
}

// TestRunBackup_OnlySources_StillErrors ensures that the half-configured
// case (sources registered but no targets) continues to surface the
// existing validation error via RunBackup, so users get feedback when
// they only partially set up backup. This parallels the Start() variant.
func TestRunBackup_OnlySources_StillErrors(t *testing.T) {
	cfg := &conf.Settings{}
	cfg.Backup.Enabled = true

	m := &Manager{
		config:     &cfg.Backup,
		fullConfig: cfg,
		sources: map[string]Source{
			"dummy": dummySource{},
		},
		targets: make(map[string]Target),
		logger:  GetLogger().Module("manager-test"),
	}

	err := m.RunBackup(t.Context())
	require.Error(t, err,
		"RunBackup should fail when sources are registered but targets are missing")
	assert.Contains(t, err.Error(), "no backup targets registered")
}

// TestRunBackup_OnlyTargets_StillErrors ensures that the mirror half-configured
// case (targets registered but no sources) also fails fast with the structured
// validation error, matching Start()'s independent sources/targets validation.
// Without the explicit no-sources check, RunBackup would silently no-op when
// the user registered targets but no sources.
func TestRunBackup_OnlyTargets_StillErrors(t *testing.T) {
	cfg := &conf.Settings{}
	cfg.Backup.Enabled = true

	m := &Manager{
		config:     &cfg.Backup,
		fullConfig: cfg,
		sources:    make(map[string]Source),
		targets: map[string]Target{
			"dummy": dummyTarget{},
		},
		logger: GetLogger().Module("manager-test"),
	}

	err := m.RunBackup(t.Context())
	require.Error(t, err,
		"RunBackup should fail when targets are registered but sources are missing")
	assert.Contains(t, err.Error(), "no backup sources registered")
}

// TestRunBackup_Disabled_NoError verifies that the explicit "manager disabled"
// early-return path returns nil without touching telemetry. This pins the
// split of the previously combined !Enabled || empty check into two separate
// early-returns, each with its own log line.
func TestRunBackup_Disabled_NoError(t *testing.T) {
	// Not parallel: the errors package keeps the hook list as package state.
	var (
		mu          sync.Mutex
		capturedErr []*errors.EnhancedError
	)
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		mu.Lock()
		defer mu.Unlock()
		capturedErr = append(capturedErr, ee)
	})
	t.Cleanup(errors.ClearErrorHooks)

	cfg := &conf.Settings{}
	cfg.Backup.Enabled = false

	m := &Manager{
		config:     &cfg.Backup,
		fullConfig: cfg,
		sources: map[string]Source{
			"dummy": dummySource{},
		},
		targets: map[string]Target{
			"dummy": dummyTarget{},
		},
		logger: GetLogger().Module("manager-test"),
	}

	require.NoError(t, m.RunBackup(t.Context()),
		"RunBackup should succeed as a silent no-op when the manager is disabled")

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, capturedErr,
		"no telemetry event should be emitted when the backup manager is disabled")
}

// dummySource is a minimal Source implementation used only for the
// half-configured test cases. It is never actually invoked because Start
// and RunBackup return before any source methods are called.
type dummySource struct{}

func (dummySource) Name() string { return "dummy" }
func (dummySource) Backup(_ context.Context) (io.ReadCloser, error) {
	return nil, nil //nolint:nilnil // never invoked in this test
}
func (dummySource) Validate() error { return nil }

// dummyTarget is a minimal Target implementation used only for the
// half-configured test cases. It is never actually invoked because Start
// and RunBackup return before any target methods are called.
type dummyTarget struct{}

func (dummyTarget) Name() string                                         { return "dummy" }
func (dummyTarget) Store(_ context.Context, _ string, _ *Metadata) error { return nil }
func (dummyTarget) List(_ context.Context) ([]BackupInfo, error)         { return nil, nil }
func (dummyTarget) Delete(_ context.Context, _ string) error             { return nil }
func (dummyTarget) Validate() error                                      { return nil }
