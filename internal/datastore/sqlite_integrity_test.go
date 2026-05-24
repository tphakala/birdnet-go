package datastore

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// newIntegrityTestStore creates a minimal SQLiteStore with an in-memory database
// for testing integrity check logic.
func newIntegrityTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	return &SQLiteStore{
		DataStore: DataStore{DB: db},
	}
}

func TestRunQuickCheck_HealthyDatabase(t *testing.T) {
	store := newIntegrityTestStore(t)
	result := store.runQuickCheck()
	assert.Equal(t, "ok", result)
}

func TestPerformStartupIntegrityCheck_HealthyDatabase(t *testing.T) {
	store := newIntegrityTestStore(t)
	store.performStartupIntegrityCheck()

	assert.False(t, store.IsCorrupted(), "healthy database should not be flagged as corrupted")

	store.integrityMu.RLock()
	assert.Equal(t, "ok", store.integrityResult)
	store.integrityMu.RUnlock()
}

func TestAttemptAutoRecovery_HealthyDatabase(t *testing.T) {
	store := newIntegrityTestStore(t)
	assert.True(t, store.attemptAutoRecovery())
	assert.False(t, store.IsCorrupted())
}

func TestTruncateResult(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "short string unchanged",
			input:    "ok",
			maxLen:   10,
			expected: "ok",
		},
		{
			name:     "exact length unchanged",
			input:    "hello",
			maxLen:   5,
			expected: "hello",
		},
		{
			name:     "long string truncated",
			input:    "this is a very long integrity check result that exceeds the limit",
			maxLen:   30,
			expected: "this is a very long integri...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, truncateResult(tt.input, tt.maxLen))
		})
	}
}

func TestCheckAndLatchCorruption_NonCorruptionError(t *testing.T) {
	store := newIntegrityTestStore(t)
	err := fmt.Errorf("some other error")
	assert.False(t, store.checkAndLatchCorruption(err, "test_op"))
	assert.False(t, store.IsCorrupted())
}

func TestCheckAndLatchCorruption_CorruptionError(t *testing.T) {
	store := newIntegrityTestStore(t)
	err := fmt.Errorf("database disk image is malformed")
	assert.True(t, store.checkAndLatchCorruption(err, "test_op"))
	assert.True(t, store.IsCorrupted())
}

func TestCheckAndLatchCorruption_OnlyLatchesOnce(t *testing.T) {
	store := newIntegrityTestStore(t)

	err := fmt.Errorf("database disk image is malformed")

	// First call latches
	assert.True(t, store.checkAndLatchCorruption(err, "op1"))
	assert.True(t, store.IsCorrupted())

	// Second call returns true (still corruption) but doesn't panic or re-latch
	assert.True(t, store.checkAndLatchCorruption(err, "op2"))
	assert.True(t, store.IsCorrupted())
}

func TestIntegrityResult_BeforeCheck(t *testing.T) {
	store := newIntegrityTestStore(t)
	result, corrupted := store.IntegrityResult()
	assert.Empty(t, result, "should be empty before any check runs")
	assert.False(t, corrupted)
}

func TestIntegrityResult_AfterHealthyCheck(t *testing.T) {
	store := newIntegrityTestStore(t)
	store.performStartupIntegrityCheck()

	result, corrupted := store.IntegrityResult()
	assert.Equal(t, "ok", result)
	assert.False(t, corrupted)
}

func TestCorruptionSentryThrottled(t *testing.T) {
	store := newIntegrityTestStore(t)
	assert.False(t, store.corruptionSentryThrottled())

	store.dbCorrupted.Store(true)
	assert.True(t, store.corruptionSentryThrottled())
}

// TestPerformStartupIntegrityCheck_CorruptedDatabase verifies the full
// corruption detection flow using a file-based database that is deliberately
// corrupted by overwriting bytes in the middle of the file.
func TestPerformStartupIntegrityCheck_CorruptedDatabase(t *testing.T) {
	// Create a file-based database with real data
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)

	// Create a table and insert rows to generate multiple pages
	require.NoError(t, db.Exec("CREATE TABLE data (id INTEGER PRIMARY KEY, value TEXT)").Error)
	for i := range 100 {
		require.NoError(t, db.Exec("INSERT INTO data (value) VALUES (?)", fmt.Sprintf("row-%d-padding-to-fill-pages", i)).Error)
	}

	// Close the database before corrupting
	sqlDB, _ := db.DB()
	require.NoError(t, sqlDB.Close())

	// Corrupt the file by overwriting bytes in the data region (after the header)
	f, err := os.OpenFile(dbPath, os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte("CORRUPT_DATA_HERE_DEADBEEF"), 4096)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Reopen with a fresh store
	db2, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB2, _ := db2.DB()
		if sqlDB2 != nil {
			_ = sqlDB2.Close()
		}
	})

	store := &SQLiteStore{
		DataStore: DataStore{DB: db2},
	}

	store.performStartupIntegrityCheck()

	// The database should be flagged as corrupted (REINDEX can't fix page corruption)
	assert.True(t, store.IsCorrupted(), "page-level corruption should be detected")

	result, corrupted := store.IntegrityResult()
	assert.True(t, corrupted)
	assert.NotEqual(t, "ok", result)
}
