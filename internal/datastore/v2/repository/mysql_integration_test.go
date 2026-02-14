//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// ============================================================================
// Complex Query Tests
// ============================================================================

func TestMySQL_Aggregation_GroupBy(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	// Insert multiple detections with different species
	species := []struct {
		scientific string
		common     string
		count      int
	}{
		{"Turdus merula", "Common Blackbird", 5},
		{"Parus major", "Great Tit", 3},
		{"Erithacus rubecula", "European Robin", 2},
	}

	for _, s := range species {
		for i := 0; i < s.count; i++ {
			_, err := testDB.ExecContext(ctx,
				"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
				s.scientific, s.common, 0.9+float64(i)*0.01,
			)
			require.NoError(t, err)
		}
	}

	// Query with GROUP BY and aggregation
	rows, err := testDB.QueryContext(ctx, `
		SELECT scientific_name, COUNT(*) as count, AVG(confidence) as avg_confidence
		FROM test_detections
		GROUP BY scientific_name
		ORDER BY count DESC
	`)
	require.NoError(t, err)
	defer rows.Close()

	results := make([]struct {
		species    string
		count      int
		avgConf    float64
	}, 0)

	for rows.Next() {
		var r struct {
			species    string
			count      int
			avgConf    float64
		}
		err := rows.Scan(&r.species, &r.count, &r.avgConf)
		require.NoError(t, err)
		results = append(results, r)
	}

	require.NoError(t, rows.Err())
	assert.Len(t, results, 3, "should have 3 species")
	assert.Equal(t, "Turdus merula", results[0].species)
	assert.Equal(t, 5, results[0].count)
}

func TestMySQL_HAVING_Clause(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	// Insert detections with varying confidence
	testData := []struct {
		species    string
		confidence float64
	}{
		{"Turdus merula", 0.95},
		{"Turdus merula", 0.85},
		{"Turdus merula", 0.75},
		{"Parus major", 0.92},
		{"Parus major", 0.88},
	}

	for _, d := range testData {
		_, err := testDB.ExecContext(ctx,
			"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
			d.species, "Test", d.confidence,
		)
		require.NoError(t, err)
	}

	// Query with HAVING clause - only species with avg confidence > 0.85
	rows, err := testDB.QueryContext(ctx, `
		SELECT scientific_name, AVG(confidence) as avg_conf
		FROM test_detections
		GROUP BY scientific_name
		HAVING AVG(confidence) > 0.85
	`)
	require.NoError(t, err)
	defer rows.Close()

	count := 0
	for rows.Next() {
		var species string
		var avgConf float64
		err := rows.Scan(&species, &avgConf)
		require.NoError(t, err)
		assert.Greater(t, avgConf, 0.85, "average confidence should be > 0.85")
		count++
	}

	require.NoError(t, rows.Err())
	assert.Equal(t, 2, count, "both species should meet criteria")
}

// ============================================================================
// Concurrent Access Tests
// ============================================================================

func TestMySQL_ConcurrentInserts(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	const numGoroutines = 10
	const insertsPerGoroutine = 5

	errChan := make(chan error, numGoroutines)
	doneChan := make(chan bool, numGoroutines)

	// Launch concurrent goroutines inserting data
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			for i := 0; i < insertsPerGoroutine; i++ {
				_, err := testDB.ExecContext(ctx,
					"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
					fmt.Sprintf("Species_%d_%d", goroutineID, i),
					fmt.Sprintf("Common_%d_%d", goroutineID, i),
					0.9,
				)
				if err != nil {
					errChan <- err
					return
				}
			}
			doneChan <- true
		}(g)
	}

	// Wait for all goroutines to complete
	for g := 0; g < numGoroutines; g++ {
		select {
		case err := <-errChan:
			t.Fatalf("concurrent insert failed: %v", err)
		case <-doneChan:
			// Success
		}
	}

	// Verify total count
	var count int
	err := testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM test_detections").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, numGoroutines*insertsPerGoroutine, count, "all inserts should succeed")
}

func TestMySQL_ConcurrentReadWrite(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	// Insert initial data
	for i := 0; i < 10; i++ {
		_, err := testDB.ExecContext(ctx,
			"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
			fmt.Sprintf("Species_%d", i), "Test", 0.9,
		)
		require.NoError(t, err)
	}

	const numReaders = 5
	const numWriters = 3
	errChan := make(chan error, numReaders+numWriters)
	doneChan := make(chan bool, numReaders+numWriters)

	// Launch readers
	for r := 0; r < numReaders; r++ {
		go func() {
			for i := 0; i < 10; i++ {
				var count int
				err := testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM test_detections").Scan(&count)
				if err != nil {
					errChan <- err
					return
				}
			}
			doneChan <- true
		}()
	}

	// Launch writers
	for w := 0; w < numWriters; w++ {
		go func(writerID int) {
			for i := 0; i < 5; i++ {
				_, err := testDB.ExecContext(ctx,
					"INSERT INTO test_detections (scientific_name, common_name, confidence) VALUES (?, ?, ?)",
					fmt.Sprintf("Writer_%d_%d", writerID, i), "Test", 0.9,
				)
				if err != nil {
					errChan <- err
					return
				}
			}
			doneChan <- true
		}(w)
	}

	// Wait for completion
	for i := 0; i < numReaders+numWriters; i++ {
		select {
		case err := <-errChan:
			t.Fatalf("concurrent operation failed: %v", err)
		case <-doneChan:
			// Success
		}
	}
}

// ============================================================================
// Foreign Key Tests
// ============================================================================

func TestMySQL_ForeignKeyConstraint(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()

	// Create parent and child tables with foreign key
	_, err := testDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS test_species (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			scientific_name VARCHAR(255) NOT NULL UNIQUE
		) ENGINE=InnoDB
	`)
	require.NoError(t, err)

	_, err = testDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS test_observations (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			species_id BIGINT NOT NULL,
			confidence FLOAT NOT NULL,
			FOREIGN KEY (species_id) REFERENCES test_species(id) ON DELETE CASCADE
		) ENGINE=InnoDB
	`)
	require.NoError(t, err)

	// Clean up at test end
	defer func() {
		_, _ = testDB.ExecContext(ctx, "DROP TABLE IF EXISTS test_observations")
		_, _ = testDB.ExecContext(ctx, "DROP TABLE IF EXISTS test_species")
	}()

	// Insert parent record
	result, err := testDB.ExecContext(ctx,
		"INSERT INTO test_species (scientific_name) VALUES (?)",
		"Turdus merula",
	)
	require.NoError(t, err)
	speciesID, _ := result.LastInsertId()

	// Insert child record (should succeed - FK exists)
	_, err = testDB.ExecContext(ctx,
		"INSERT INTO test_observations (species_id, confidence) VALUES (?, ?)",
		speciesID, 0.95,
	)
	assert.NoError(t, err, "insert with valid FK should succeed")

	// Try to insert child with non-existent FK (should fail)
	_, err = testDB.ExecContext(ctx,
		"INSERT INTO test_observations (species_id, confidence) VALUES (?, ?)",
		99999, 0.95,
	)
	assert.Error(t, err, "insert with invalid FK should fail")
	assert.Contains(t, err.Error(), "foreign key constraint", "error should mention FK constraint")

	// Verify CASCADE DELETE
	var obsCount int
	err = testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM test_observations").Scan(&obsCount)
	require.NoError(t, err)
	assert.Equal(t, 1, obsCount, "should have 1 observation")

	// Delete parent (should cascade)
	_, err = testDB.ExecContext(ctx, "DELETE FROM test_species WHERE id = ?", speciesID)
	require.NoError(t, err)

	// Verify child was deleted
	err = testDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM test_observations").Scan(&obsCount)
	require.NoError(t, err)
	assert.Equal(t, 0, obsCount, "observation should be cascade deleted")
}

// TODO: Additional test ideas for future implementation:
// - Subquery tests with IN/EXISTS
// - Index usage verification with EXPLAIN
// - Deadlock detection and recovery
// - Connection pool exhaustion scenarios
// - JSON column operations (MySQL 5.7+)
// - Full-text search capabilities
