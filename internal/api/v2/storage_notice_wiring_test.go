package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// TestStorageNoticeInputsChanged covers the settings gate that re-evaluates the
// storage persistence notices: each of the five path inputs flips it, and an
// unrelated field does not.
func TestStorageNoticeInputsChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(s *conf.Settings)
		want   bool
	}{
		{"models directory", func(s *conf.Settings) { s.Models.Directory = "/models" }, true},
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

	// An injected notification service stands in for the process-wide one. The arm
	// count only reflects the facade's own start and schedule calls.
	notifService := notification.NewService(notification.DefaultServiceConfig())
	t.Cleanup(notifService.Stop)

	c, err := NewWithOptions(echo.New(), mockDS, settings, nil, nil, make(chan string, 10), metrics, initializeRoutes,
		WithNotificationService(notifService))
	require.NoError(t, err)
	return c
}

// TestStorageNoticeWiring drives the real system handler: a routed controller
// starts the storage notices, a settings save that changes a path schedules a
// re-evaluation, and Shutdown stops them.
func TestStorageNoticeWiring(t *testing.T) {
	testutil.VerifyNoLeaks(t)

	unrouted := newStorageWiringController(t, false)
	assert.Zero(t, unrouted.system.StorageNoticeSyncStatus().Arms, "no background evaluation without routes (tests)")
	unrouted.Shutdown()
	assert.True(t, unrouted.system.StorageNoticeSyncStatus().Stopped, "Shutdown stops even an unrouted controller")

	routed := newStorageWiringController(t, true)
	if routed.goroutinesStarted != nil {
		<-routed.goroutinesStarted
	}
	assert.Equal(t, 1, routed.system.StorageNoticeSyncStatus().Arms, "a routed controller arms the startup evaluation")

	base := routed.CurrentSettings()
	moved := *base
	moved.Realtime.Audio.Export.Path = base.Realtime.Audio.Export.Path + "-moved"
	require.NoError(t, routed.handleSettingsChanges(base, &moved))
	assert.Equal(t, 2, routed.system.StorageNoticeSyncStatus().Arms, "an export path change schedules a re-evaluation")

	require.NoError(t, routed.handleSettingsChanges(base, base))
	assert.Equal(t, 2, routed.system.StorageNoticeSyncStatus().Arms, "a save that changes no storage path schedules nothing")

	routed.Shutdown()
	assert.True(t, routed.system.StorageNoticeSyncStatus().Stopped, "Shutdown stops the storage notices")
}
