package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Compile-time interface compliance check.
var _ app.Service = (*APIServerService)(nil)

func TestAPIServerService_Name(t *testing.T) {
	t.Parallel()

	svc := NewAPIServerService(&conf.Settings{}, nil, nil, nil, nil)
	assert.Equal(t, "api-server", svc.Name())
}

func TestAPIServerService_Start_FailsFastWithNilDataStore(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	bn := NewBirdNETAnalyzer(settings)
	db := NewDatabaseService(settings, nil)
	// db.DataStore() returns nil since Start() was never called.
	metrics, _ := observability.NewMetrics()

	svc := NewAPIServerService(settings, bn, db, metrics, nil)
	err := svc.Start(t.Context())
	require.Error(t, err, "Start() should fail when DataStore is nil")
	assert.Contains(t, err.Error(), "datastore", "error should mention datastore")
}

func TestAPIServerService_Start_FailsFastWithNilBirdNET(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	bn := NewBirdNETAnalyzer(settings)
	// bn.BirdNET() returns nil since Start() was never called.

	db := NewDatabaseService(settings, nil)
	// Set a non-nil dataStore so the first check passes.
	db.dataStore = mocks.NewMockInterface(t)

	metrics, _ := observability.NewMetrics()

	svc := NewAPIServerService(settings, bn, db, metrics, nil)
	err := svc.Start(t.Context())
	require.Error(t, err, "Start() should fail when BirdNET is nil")
	assert.Contains(t, err.Error(), "birdnet", "error should mention birdnet")
}

func TestAPIServerService_Stop_NilSafe(t *testing.T) {
	t.Parallel()

	svc := NewAPIServerService(&conf.Settings{}, nil, nil, nil, nil)
	// Stop before Start should not panic and should return nil.
	assert.NotPanics(t, func() {
		err := svc.Stop(t.Context())
		assert.NoError(t, err)
	})
}

func TestAPIServerService_GettersBeforeStart(t *testing.T) {
	t.Parallel()

	svc := NewAPIServerService(&conf.Settings{}, nil, nil, nil, nil)
	assert.Nil(t, svc.Processor(), "Processor() should return nil before Start()")
	assert.Nil(t, svc.Metrics(), "Metrics() should return nil before Start()")
	assert.Nil(t, svc.APIController(), "APIController() should return nil before Start()")
	assert.Nil(t, svc.SunCalc(), "SunCalc() should return nil before Start()")
}
