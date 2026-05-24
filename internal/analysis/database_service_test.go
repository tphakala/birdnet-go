package analysis

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Compile-time interface compliance check.
var _ app.Service = (*DatabaseService)(nil)
var _ app.TieredService = (*DatabaseService)(nil)

func TestDatabaseService_Name(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	assert.Equal(t, "database", svc.Name())
}

func TestDatabaseService_ShutdownTier(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	assert.Equal(t, app.TierCore, svc.ShutdownTier())
}

func TestDatabaseService_GettersBeforeStart(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	assert.Nil(t, svc.DataStore(), "DataStore() should return nil before Start()")
	assert.Nil(t, svc.V2Manager(), "V2Manager() should return nil before Start()")
	assert.False(t, svc.IsV2OnlyMode(), "IsV2OnlyMode() should return false before Start()")
}

func TestDatabaseService_Stop_NilSafe(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	// Stop before Start should not panic and should return nil.
	assert.NotPanics(t, func() {
		err := svc.Stop(t.Context())
		assert.NoError(t, err)
	})
}

// TestErrV2SchemaCorrupted_PropagatesThroughInitializeWrapChain pins the unwrap
// behavior the silent-fallback prevention in Start() depends on. validateSchemaIntegrity
// wraps the sentinel with fmt.Errorf, SQLiteManager.Initialize wraps that again, and
// initializeV2OnlyMode / v2only.InitializeFreshInstall wrap the result with the internal
// errors package. errors.Is must unwrap through all three layers and still match the
// sentinel; otherwise Start() would silently fall back to legacy mode for corrupted
// databases (GitHub #3211).
func TestErrV2SchemaCorrupted_PropagatesThroughInitializeWrapChain(t *testing.T) {
	t.Parallel()

	// Layer 1: validator returns the sentinel wrapped with details.
	validatorErr := fmt.Errorf("%w: detections missing columns [unlikely]", datastoreV2.ErrV2SchemaCorrupted)
	// Layer 2: Manager.Initialize wraps the validator error.
	initializeErr := fmt.Errorf("v2 schema integrity check failed: %w", validatorErr)
	// Layer 3: initializeV2OnlyMode wraps the manager error with the internal errors
	// package. EnhancedError.Unwrap() must keep the chain intact.
	wrapped := errors.New(initializeErr).
		Component("analysis").
		Category(errors.CategoryDatabase).
		Context("operation", "initialize_v2_database").
		Build()

	assert.True(t, errors.Is(wrapped, datastoreV2.ErrV2SchemaCorrupted),
		"errors.Is must unwrap through fmt.Errorf and EnhancedError to find the sentinel")
}

func TestDatabaseService_Start_FreshInstall(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "metrics initialization should succeed")

	svc := NewDatabaseService(settings, metrics)
	err = svc.Start(t.Context())
	require.NoError(t, err, "Start() should succeed for fresh install")
	t.Cleanup(func() {
		assert.NoError(t, svc.Stop(t.Context()), "Stop() should succeed")
	})

	// DataStore should be set after Start.
	assert.NotNil(t, svc.DataStore(), "DataStore() should not be nil after Start()")

	// Fresh install goes to v2-only mode.
	assert.True(t, svc.IsV2OnlyMode(), "should be in v2-only mode for fresh install")
	assert.NotNil(t, svc.V2Manager(), "V2Manager() should be set for fresh install")
}
