package datastore

import (
	"context"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// countCallPattern matches a COUNT( aggregate but not the page_count() pragma
// function that the size query uses.
var countCallPattern = regexp.MustCompile(`(?i)\bcount\(`)

// statementRecorder is a GORM logger that records every executed SQL
// statement, including statements that fail.
type statementRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (r *statementRecorder) LogMode(gormlogger.LogLevel) gormlogger.Interface { return r }
func (r *statementRecorder) Info(context.Context, string, ...any)             {}
func (r *statementRecorder) Warn(context.Context, string, ...any)             {}
func (r *statementRecorder) Error(context.Context, string, ...any)            {}

func (r *statementRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements = append(r.statements, sql)
}

func (r *statementRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.statements)
}

func TestCollectDatabaseStats_UpdatesSizeWithoutCountQueries(t *testing.T) {
	t.Parallel()

	ds := setupTestDB(t)
	recorder := &statementRecorder{}
	ds.DB = ds.DB.Session(&gorm.Session{Logger: recorder})

	reg := prometheus.NewRegistry()
	m, err := metrics.NewDatastoreMetrics(reg)
	require.NoError(t, err)
	ds.SetMetrics(m)

	ds.collectDatabaseStats(t.Context())

	for _, stmt := range recorder.recorded() {
		assert.False(t, countCallPattern.MatchString(stmt), "periodic stats tick must not scan tables: %s", stmt)
	}

	families, err := reg.Gather()
	require.NoError(t, err)
	var sizeBytes float64
	for _, f := range families {
		if f.GetName() == "datastore_db_size_bytes" && len(f.GetMetric()) > 0 {
			sizeBytes = f.GetMetric()[0].GetGauge().GetValue()
		}
	}
	assert.Positive(t, sizeBytes, "database size gauge must be updated")
}
