package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/testutil"
	"go.uber.org/goleak"
)

// TestOptimizeNoticeInputsChanged covers the settings gate that re-evaluates the
// model optimize notice: a location change (including a first-time location
// set), a ModelRegion change or a primary model path change triggers it; an
// unchanged save does not.
func TestOptimizeNoticeInputsChanged(t *testing.T) {
	t.Parallel()

	withRegion := func(s *conf.Settings, region string) *conf.Settings {
		s.BirdNET.ModelRegion = region
		return s
	}
	withModelPath := func(s *conf.Settings, path string) *conf.Settings {
		s.BirdNET.ModelPath = path
		return s
	}
	tests := []struct {
		name string
		old  *conf.Settings
		next *conf.Settings
		want bool
	}{
		{"identical", regionTestSettings(60.17, 24.94, true), regionTestSettings(60.17, 24.94, true), false},
		{"coordinates changed", regionTestSettings(60.17, 24.94, true), regionTestSettings(4.61, -74.08, true), true},
		{"location configured first time", regionTestSettings(0, 0, false), regionTestSettings(0, 0, true), true},
		{
			"model region changed, same location",
			withRegion(regionTestSettings(60.17, 24.94, true), "auto"),
			withRegion(regionTestSettings(60.17, 24.94, true), "global"),
			true,
		},
		{
			"custom primary model configured",
			regionTestSettings(60.17, 24.94, true),
			withModelPath(regionTestSettings(60.17, 24.94, true), "/data/models/my-birdnet.tflite"),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, optimizeNoticeInputsChanged(tt.old, tt.next))
		})
	}
}

// newOptimizeWiringController builds a controller with a ModelManager wired, as
// the analyzer does, with or without route initialization.
func newOptimizeWiringController(t *testing.T, initializeRoutes bool) *Controller {
	t.Helper()
	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().PruneAppEvents(mock.Anything, mock.Anything).Return(int64(0), nil).Maybe()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = t.TempDir()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err)
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)

	c, err := NewWithOptions(echo.New(), mockDS, settings, nil, nil, make(chan string, 10), metrics,
		initializeRoutes, WithModelManager(mm))
	require.NoError(t, err)
	return c
}

// TestOptimizeNoticeWiring verifies the facade publishes the models handler for
// the optimize notice only on a routed controller with a ModelManager, and that
// the topology, settings and shutdown hooks drive it without leaking goroutines.
func TestOptimizeNoticeWiring(t *testing.T) {
	testutil.VerifyNoLeaks(t,
		goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
	)

	unrouted := newOptimizeWiringController(t, false)
	assert.Nil(t, unrouted.optimizeNotices.Load(), "no background evaluation without routes (tests)")
	unrouted.Shutdown()

	routed := newOptimizeWiringController(t, true)
	if routed.goroutinesStarted != nil {
		<-routed.goroutinesStarted
	}
	require.Same(t, routed.models, routed.optimizeNotices.Load(),
		"a routed controller with a ModelManager publishes the models handler")

	// Topology and settings hooks schedule a re-evaluation; Shutdown cancels it.
	routed.OnModelTopologyChanged()
	routed.scheduleOptimizeNoticeSync()
	routed.Shutdown()
}
