//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// MySQL test container shared across all tests in this package
var (
	mysqlContainer *containers.MySQLContainer
	testDB         *sql.DB
)

// TestMain sets up the MySQL container for all tests in this package
func TestMain(m *testing.M) {
	var err error

	// Create MySQL container
	mysqlContainer, err = containers.NewMySQLContainer(nil) // Use defaults
	if err != nil {
		panic("failed to create MySQL container: " + err.Error())
	}

	// Get database connection
	testDB = mysqlContainer.GetDB(&testing.T{}) // Pass dummy *testing.T for GetDB

	// Run migrations
	if err := runMigrations(testDB); err != nil {
		mysqlContainer.Terminate()
		panic("failed to run migrations: " + err.Error())
	}

	// Run tests
	code := m.Run()

	// Cleanup
	if err := mysqlContainer.Terminate(); err != nil {
		panic("failed to terminate MySQL container: " + err.Error())
	}

	os.Exit(code)
}

// runMigrations applies the database schema to the test MySQL database
func runMigrations(db *sql.DB) error {
	// TODO: Apply actual schema migrations here
	// For now, this is a placeholder that would execute SQL schema files
	ctx := context.Background()

	// Example: Create a simple test table
	schema := `
		CREATE TABLE IF NOT EXISTS test_detections (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			scientific_name VARCHAR(255) NOT NULL,
			common_name VARCHAR(255),
			confidence FLOAT NOT NULL,
			detected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_scientific_name (scientific_name),
			INDEX idx_detected_at (detected_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`

	_, err := db.ExecContext(ctx, schema)
	return err
}

// resetDatabase truncates all tables to ensure test isolation
func resetDatabase(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	err := mysqlContainer.Reset(ctx, []string{"test_detections"})
	require.NoError(t, err, "failed to reset database")
}

// ============================================================================
// Basic CRUD Tests
// ============================================================================

func TestMySQL_InsertAndSelect(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	// Insert a test detection
	result, err := testDB.ExecContext(ctx,
		"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
		"Turdus merula", "Common Blackbird", 0.95,
	)
	require.NoError(t, err, "failed to insert detection")

	id, err := result.LastInsertId()
	require.NoError(t, err, "failed to get last insert ID")
	assert.Greater(t, id, int64(0), "ID should be positive")

	// Select the inserted detection
	var scientificName, commonName string
	var confidence float64
	err = testDB.QueryRowContext(ctx,
		"SELECT scientific_name, common_name, confidence FROM test_detections WHERE id = ?",
		id,
	).Scan(&scientificName, &commonName, &confidence)

	require.NoError(t, err, "failed to select detection")
	assert.Equal(t, "Turdus merula", scientificName)
	assert.Equal(t, "Common Blackbird", commonName)
	assert.Equal(t, 0.95, confidence)
}

func TestMySQL_Update(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	// Insert
	result, err := testDB.ExecContext(ctx,
		"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
		"Turdus merula", "Common Blackbird", 0.85,
	)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	// Update
	_, err = testDB.ExecContext(ctx,
		"UPDATE test_detections SET confidence = ? WHERE id = ?",
		0.95, id,
	)
	require.NoError(t, err, "failed to update detection")

	// Verify
	var confidence float64
	err = testDB.QueryRowContext(ctx,
		"SELECT confidence FROM test_detections WHERE id = ?",
		id,
	).Scan(&confidence)

	require.NoError(t, err)
	assert.Equal(t, 0.95, confidence)
}

func TestMySQL_Delete(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	// Insert
	result, err := testDB.ExecContext(ctx,
		"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
		"Turdus merula", "Common Blackbird", 0.95,
	)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	// Delete
	_, err = testDB.ExecContext(ctx,
		"DELETE FROM test_detections WHERE id = ?",
		id,
	)
	require.NoError(t, err, "failed to delete detection")

	// Verify deletion
	var count int
	err = testDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM test_detections WHERE id = ?",
		id,
	).Scan(&count)

	require.NoError(t, err)
	assert.Equal(t, 0, count, "detection should be deleted")
}

// ============================================================================
// Transaction Tests
// ============================================================================

func TestMySQL_Transaction_Commit(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	tx, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err, "failed to begin transaction")

	// Insert within transaction
	_, err = tx.ExecContext(ctx,
		"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
		"Parus major", "Great Tit", 0.92,
	)
	require.NoError(t, err)

	// Commit
	err = tx.Commit()
	require.NoError(t, err, "failed to commit transaction")

	// Verify data persisted
	var count int
	err = testDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM test_detections WHERE scientific_name = ?",
		"Parus major",
	).Scan(&count)

	require.NoError(t, err)
	assert.Equal(t, 1, count, "detection should be committed")
}

func TestMySQL_Transaction_Rollback(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	tx, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err, "failed to begin transaction")

	// Insert within transaction
	_, err = tx.ExecContext(ctx,
		"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
		"Erithacus rubecula", "European Robin", 0.88,
	)
	require.NoError(t, err)

	// Rollback
	err = tx.Rollback()
	require.NoError(t, err, "failed to rollback transaction")

	// Verify data not persisted
	var count int
	err = testDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM test_detections WHERE scientific_name = ?",
		"Erithacus rubecula",
	).Scan(&count)

	require.NoError(t, err)
	assert.Equal(t, 0, count, "detection should be rolled back")
}

// ============================================================================
// Connection Pool Tests
// ============================================================================

func TestMySQL_ConnectionPool_HealthCheck(t *testing.T) {
	ctx := context.Background()

	// Verify health check passes
	err := mysqlContainer.HealthCheck(ctx)
	assert.NoError(t, err, "health check should pass")

	// Verify we can still query
	var result int
	err = testDB.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	require.NoError(t, err)
	assert.Equal(t, 1, result)
}

// ============================================================================
// MySQL-Specific Feature Tests
// ============================================================================

func TestMySQL_AutoIncrement(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	// Insert multiple records
	ids := make([]int64, 3)
	for i := 0; i < 3; i++ {
		result, err := testDB.ExecContext(ctx,
			"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
			"Species"+string(rune('A'+i)), "Common"+string(rune('A'+i)), 0.9,
		)
		require.NoError(t, err)
		ids[i], err = result.LastInsertId()
		require.NoError(t, err)
	}

	// Verify IDs are sequential
	assert.Equal(t, ids[0]+1, ids[1], "IDs should be sequential")
	assert.Equal(t, ids[1]+1, ids[2], "IDs should be sequential")
}

// TODO: Add more comprehensive tests:
// - Complex queries with JOINs
// - Subqueries and aggregations
// - Concurrent access patterns
// - Foreign key constraints
// - Index usage verification
// - Deadlock detection and recovery
