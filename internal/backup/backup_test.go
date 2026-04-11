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

// dummySource is a minimal Source implementation used only for the
// half-configured test case. It is never actually invoked because Start
// returns before any source methods are called.
type dummySource struct{}

func (dummySource) Name() string { return "dummy" }
func (dummySource) Backup(_ context.Context) (io.ReadCloser, error) {
	return nil, nil //nolint:nilnil // never invoked in this test
}
func (dummySource) Validate() error { return nil }
