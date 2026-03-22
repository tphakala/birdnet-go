package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
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

func TestDatabaseService_Start_Placeholder(t *testing.T) {
	t.Parallel()

	svc := NewDatabaseService(&conf.Settings{}, nil)
	err := svc.Start(t.Context())
	assert.NoError(t, err, "Start() placeholder should return nil")
}
