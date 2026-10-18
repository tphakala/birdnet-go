package api

import (
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// TestStorageNoticeInputsChanged covers the settings gate that re-evaluates the
// storage persistence notices: each of the six path inputs flips it, and an
// unrelated field does not.
func TestStorageNoticeInputsChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(s *conf.Settings)
		want   bool
	}{
		{"models directory", func(s *conf.Settings) { s.Models.Directory = "/models" }, true},
		{"sqlite enabled", func(s *conf.Settings) { s.Output.SQLite.Enabled = !s.Output.SQLite.Enabled }, true},
		{"sqlite path", func(s *conf.Settings) { s.Output.SQLite.Path = "/data/other.db" }, true},
		{"mysql enabled", func(s *conf.Settings) { s.Output.MySQL.Enabled = !s.Output.MySQL.Enabled }, true},
		{"export enabled", func(s *conf.Settings) { s.Realtime.Audio.Export.Enabled = !s.Realtime.Audio.Export.Enabled }, true},
		{"export path", func(s *conf.Settings) { s.Realtime.Audio.Export.Path = "/data/clips" }, true},
		{"location", func(s *conf.Settings) { s.BirdNET.Latitude = 12.5 }, false},
		{"nothing", func(*conf.Settings) {}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			oldSettings := &conf.Settings{}
			cur := &conf.Settings{}
			tt.mutate(cur)
			assert.Equal(t, tt.want, storageNoticeInputsChanged(oldSettings, cur))
		})
	}
}

// newStorageWiringController builds a controller with or without route
// initialization.
func newStorageWiringController(t *testing.T, initializeRoutes bool) *Controller {
	t.Helper()
	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().PruneAppEvents(mock.Anything, mock.Anything).Return(int64(0), nil).Maybe()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = t.TempDir()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	c, err := NewWithOptions(echo.New(), mockDS, settings, nil, nil, make(chan string, 10), metrics, initializeRoutes)
	require.NoError(t, err)
	return c
}

// storageRecorder counts the facade's storage notice calls.
type storageRecorder struct {
	mu        sync.Mutex
	schedules int
	stops     int
}

func (r *storageRecorder) ScheduleStorageNoticeSync() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schedules++
}

func (r *storageRecorder) StopStorageNoticeSync() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops++
}

func (r *storageRecorder) counts() (schedules, stops int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.schedules, r.stops
}

// TestStorageNoticeWiring verifies the facade publishes the system handler for
// the storage notices only on a routed controller, that a path change in a
// settings save schedules a re-evaluation, and that Shutdown stops it.
func TestStorageNoticeWiring(t *testing.T) {
	testutil.VerifyNoLeaks(t)

	unrouted := newStorageWiringController(t, false)
	assert.Nil(t, unrouted.storageNotices.Load(), "no background evaluation without routes (tests)")
	unrouted.Shutdown()

	routed := newStorageWiringController(t, true)
	if routed.goroutinesStarted != nil {
		<-routed.goroutinesStarted
	}
	hook := routed.storageNotices.Load()
	require.NotNil(t, hook, "a routed controller publishes the system handler")
	require.Same(t, routed.system, hook.scheduler)
	// The real handler armed its startup evaluation; stop it so only the
	// recorder sees the calls below.
	routed.system.StopStorageNoticeSync()

	rec := &storageRecorder{}
	routed.storageNotices.Store(&storageNoticeHook{scheduler: rec})

	base := routed.CurrentSettings()
	moved := *base
	moved.Realtime.Audio.Export.Path = base.Realtime.Audio.Export.Path + "-moved"
	require.NoError(t, routed.handleSettingsChanges(base, &moved))
	schedules, _ := rec.counts()
	assert.Equal(t, 1, schedules, "an export path change schedules a re-evaluation")

	require.NoError(t, routed.handleSettingsChanges(base, base))
	schedules, _ = rec.counts()
	assert.Equal(t, 1, schedules, "a save that changes no storage path schedules nothing")

	routed.Shutdown()
	_, stops := rec.counts()
	assert.Equal(t, 1, stops, "Shutdown stops the storage notice scheduler")
}
