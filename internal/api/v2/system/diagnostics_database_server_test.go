package system

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/health"
)

// dialectManagerStub reports a dialect and a handle; nothing else is called.
type dialectManagerStub struct {
	datastoreV2.Manager
	mysql bool
	db    *gorm.DB
}

func (s dialectManagerStub) IsMySQL() bool { return s.mysql }
func (s dialectManagerStub) DB() *gorm.DB  { return s.db }

func TestMySQLServerDB_OnlyForMySQL(t *testing.T) {
	handle := &gorm.DB{}
	tests := []struct {
		name    string
		manager datastoreV2.Manager
		want    *gorm.DB
	}{
		{"no v2 manager", nil, nil},
		{"sqlite manager", dialectManagerStub{mysql: false, db: handle}, nil},
		{"mysql manager", dialectManagerStub{mysql: true, db: handle}, handle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{Core: apitest.NewCore(t)}
			h.V2Manager = tt.manager
			assert.Same(t, tt.want, h.mysqlServerDB())
		})
	}
}

func TestRegisterHealthChecks_DatabaseServerChecksSkippedWithoutMySQL(t *testing.T) {
	h := &Handler{Core: apitest.NewCore(t)}
	h.V2Manager = nil
	h.DS = nil // the other database checks then report unknown instead of calling the mock
	h.healthRegistry = health.NewRegistry()
	h.registerHealthChecks()

	statuses := map[string]health.Status{}
	for _, r := range h.healthRegistry.RunCategory(t.Context(), health.CategoryDatabase) {
		statuses[r.Name] = r.Status
	}
	require.Contains(t, statuses, "database_server_version")
	require.Contains(t, statuses, "database_charset")
	assert.Equal(t, health.StatusSkipped, statuses["database_server_version"])
	assert.Equal(t, health.StatusSkipped, statuses["database_charset"])
}
