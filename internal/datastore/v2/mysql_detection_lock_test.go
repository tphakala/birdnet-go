//go:build integration

package v2

import (
	"database/sql"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	// lockRaceWaitTimeout bounds how long the test waits for the second lock to
	// block behind the first.
	lockRaceWaitTimeout = 10 * time.Second
	// lockRacePollInterval is the polling tick for the lock wait. InnoDB serves
	// information_schema.innodb_trx from a cache it refreshes only when the
	// previous read is at least 100 ms old, so a faster poll keeps seeing the
	// first snapshot and can miss the wait.
	lockRacePollInterval = 200 * time.Millisecond
	// lockRaceRounds is the number of detections locked concurrently at the
	// default isolation level.
	lockRaceRounds = 30
	// lockRaceWorkers is the number of concurrent Lock calls per detection.
	lockRaceWorkers = 4
	// lockRaceMissingID is a detection id that no test creates.
	lockRaceMissingID uint = 99999999
	// lockRaceLabelName is the species of the seeded label.
	lockRaceLabelName = "Parus major"
	// lockRaceRetryMetric is the counter RetryOnLock increments for every retry.
	lockRaceRetryMetric = "datastore_db_transaction_retries_total"
	// lockRaceWaitQuery finds a transaction that is blocked on a row lock.
	lockRaceWaitQuery = "SELECT COUNT(*) FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = ? AND trx_state = 'LOCK WAIT'"
)

// lockRaceFirstLockedAt is the fixed past lock time given to the first lock, so
// an implementation that rewrites locked_at on the second lock is caught even
// when both locks fall in the same millisecond.
var lockRaceFirstLockedAt = time.Date(2025, 3, 3, 9, 30, 15, 0, time.UTC)

// openLockRaceRootDB opens a root connection, which can read
// information_schema.innodb_trx. The testcontainers MySQL module sets the root
// password to the test user's password, as the v2only tests rely on too.
func openLockRaceRootDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := (&mysqldriver.Config{
		User:   "root",
		Passwd: coexTestPassword,
		Net:    "tcp",
		Addr:   net.JoinHostPort(coexTestHost, coexTestPort),
		// MariaDB's root account uses mysql_native_password, which the driver
		// only offers when asked.
		AllowNativePasswords: true,
	}).FormatDSN()
	root, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	require.NoError(t, root.PingContext(t.Context()), "root connection needed to read innodb_trx")
	return root
}

// TestMySQL_Lock_ConcurrentLocksOfOneDetection pins that locking one detection
// from several sessions at once succeeds for every caller, under both table
// layouts. The old INSERT ... WHERE NOT EXISTS failed the loser with a duplicate
// key at READ COMMITTED, and with a deadlock at REPEATABLE READ that MariaDB
// 10.11 to 11.8 reports as Error 1467, which is not retried.
func TestMySQL_Lock_ConcurrentLocksOfOneDetection(t *testing.T) {
	root := openLockRaceRootDB(t)

	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)
			mgr := newTestMySQLManager(t, useV2Prefix)
			t.Cleanup(func() {
				_ = mgr.Delete()
				_ = mgr.Close()
			})
			require.NoError(t, mgr.Initialize())
			db := mgr.DB()
			ctx := t.Context()

			labelType := entities.LabelType{Name: "lock_race_test"}
			require.NoError(t, db.Create(&labelType).Error)
			model := entities.AIModel{Name: "LockRace", Version: "1", ModelType: entities.ModelTypeBird}
			require.NoError(t, db.Create(&model).Error)
			label := &entities.Label{ScientificName: lockRaceLabelName, ModelID: model.ID, LabelTypeID: labelType.ID}
			require.NoError(t, db.Create(label).Error)
			addDetection := func() uint {
				d := &entities.Detection{LabelID: label.ID, ModelID: model.ID, Confidence: 0.8, DetectedAt: 1000}
				require.NoError(t, db.Create(d).Error)
				return d.ID
			}
			// Real metrics, so a lock error that RetryOnLock retries and hides
			// (a deadlock on MySQL) still shows up as a counted retry.
			reg := prometheus.NewRegistry()
			lockMetrics, err := metrics.NewDatastoreMetrics(reg)
			require.NoError(t, err)
			repo := repository.NewDetectionRepository(db, lockMetrics, useV2Prefix, true)
			lockRows := func(id uint) []entities.DetectionLock {
				var locks []entities.DetectionLock
				require.NoError(t, db.Where("detection_id = ?", id).Find(&locks).Error)
				return locks
			}

			t.Run("the loser of a read committed race succeeds", func(t *testing.T) {
				id := addDetection()

				// Deferred calls run last in, first out: the first transaction ends
				// first, which lets the goroutine finish, and only then is the second
				// one rolled back. Rolling the second back while its statement still
				// waits would block until the server's lock wait times out.
				second := db.Begin(&sql.TxOptions{Isolation: sql.LevelReadCommitted})
				require.NoError(t, second.Error)
				defer func() { _ = second.Rollback() }()
				var connID uint64
				require.NoError(t, second.Raw("SELECT CONNECTION_ID()").Scan(&connID).Error)
				var wg sync.WaitGroup
				defer wg.Wait()

				first := db.Begin()
				require.NoError(t, first.Error)
				defer func() { _ = first.Rollback() }()
				require.NoError(t, repository.NewDetectionRepository(first, nil, useV2Prefix, true).Lock(ctx, id))
				require.NoError(t, first.Model(&entities.DetectionLock{}).
					Where("detection_id = ?", id).UpdateColumn("locked_at", lockRaceFirstLockedAt).Error)

				result := make(chan error, 1)
				wg.Go(func() {
					result <- repository.NewDetectionRepository(second, nil, useV2Prefix, true).Lock(ctx, id)
				})
				// The condition runs in its own goroutine, so it must not use require.
				require.Eventually(t, func() bool {
					var waiting int
					if err := root.QueryRowContext(ctx, lockRaceWaitQuery, connID).Scan(&waiting); err != nil {
						return false
					}
					return waiting > 0
				}, lockRaceWaitTimeout, lockRacePollInterval, "the second lock never waited for the first")
				require.NoError(t, first.Commit().Error)
				require.NoError(t, <-result, "the second lock must succeed once the first commits")
				require.NoError(t, second.Commit().Error)

				locks := lockRows(id)
				require.Len(t, locks, 1)
				assert.True(t, locks[0].LockedAt.Equal(lockRaceFirstLockedAt), "the first lock time must stay, got %s", locks[0].LockedAt)
			})

			t.Run("concurrent locks at the default isolation level succeed", func(t *testing.T) {
				for range lockRaceRounds {
					id := addDetection()
					errs := make(chan error, lockRaceWorkers)
					var wg sync.WaitGroup
					for range lockRaceWorkers {
						wg.Go(func() { errs <- repo.Lock(ctx, id) })
					}
					wg.Wait()
					close(errs)
					for err := range errs {
						require.NoError(t, err)
					}
					assert.Len(t, lockRows(id), 1)
				}
				assert.Zero(t, retryCount(t, reg), "concurrent locks must not hit a lock error that RetryOnLock retries")
			})

			t.Run("a missing detection is not found", func(t *testing.T) {
				require.ErrorIs(t, repo.Lock(ctx, lockRaceMissingID), repository.ErrDetectionNotFound)
				assert.Empty(t, lockRows(lockRaceMissingID))
			})
		})
	}
}

// retryCount sums the lock retry counter over all label combinations.
func retryCount(t *testing.T, reg *prometheus.Registry) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	var total float64
	for _, f := range families {
		if f.GetName() != lockRaceRetryMetric {
			continue
		}
		for _, m := range f.GetMetric() {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}
