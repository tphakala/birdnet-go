//go:build integration

package v2only

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// setupMySQLDatastore starts a MySQL testcontainer and builds a fully-wired
// v2only Datastore against it. It returns the datastore (timezone forced to UTC
// for deterministic date bucketing) and registers container/datastore cleanup.
func setupMySQLDatastore(t *testing.T) *Datastore {
	t.Helper()

	ctx := t.Context()

	cfg := &containers.MySQLConfig{
		Database:     "birdnet_like_escape",
		RootPassword: "test",
		Username:     "testuser",
		Password:     "testpass",
		ImageTag:     "8.0",
	}
	container, err := containers.NewMySQLContainer(ctx, cfg)
	require.NoError(t, err, "failed to start MySQL container")
	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is canceled
		// just before cleanups run, so reusing it would abort termination.
		if err := container.Terminate(context.Background()); err != nil { //nolint:gocritic // cleanup must outlive the test context
			t.Logf("failed to terminate MySQL container: %v", err)
		}
	})

	host, err := container.GetHost(ctx)
	require.NoError(t, err)
	port, err := container.GetPort(ctx)
	require.NoError(t, err)

	testLogger := logger.NewConsoleLogger("v2only_mysql_test", logger.LogLevelDebug)
	manager, err := v2.NewMySQLManager(&v2.MySQLConfig{
		Host:     host,
		Port:     strconv.Itoa(port),
		Username: cfg.Username,
		Password: cfg.Password,
		Database: cfg.Database,
		Logger:   testLogger,
	})
	require.NoError(t, err, "failed to create MySQL manager")
	require.NoError(t, manager.Initialize(), "failed to initialize MySQL schema")

	// isMySQL=true selects the MySQL dialect in every repository constructor.
	dsCfg := buildConfigForManager(t, manager, testLogger, true, nil)
	ds, err := New(dsCfg)
	require.NoError(t, err, "failed to create v2only datastore")
	t.Cleanup(func() { _ = ds.Close() })

	// Force UTC so detection date bucketing is deterministic and independent of
	// the test host's local zone.
	ds.timezone = time.UTC
	return ds
}

// TestV2OnlyDatastore_GetSpeciesLastDetectionDateBefore_MySQL reproduces the MySQL
// Error 1064 syntax error in the LIKE ... ESCAPE clause. The original code used a
// backslash escape character; MySQL's default sql_mode treats a lone backslash in
// a string literal as an escape, so the literal never terminates and the server
// returns Error 1064. The bug is invisible on SQLite (backslash is not special
// there), which is why the SQLite-only test path masked it. Exercising the query
// against a real MySQL backend makes the syntax error surface.
func TestV2OnlyDatastore_GetSpeciesLastDetectionDateBefore_MySQL(t *testing.T) {
	ds := setupMySQLDatastore(t)
	ctx := t.Context()

	const cutoff = "2024-06-15"
	day := func(d int) time.Time { return time.Date(2024, 6, d, 12, 0, 0, 0, time.UTC) }

	t.Run("exact match does not raise error 1064", func(t *testing.T) {
		// The core regression assertion: before the fix this call returns
		// "Error 1064 (42000): You have an error in your SQL syntax".
		seedDetection(t, ds, "Turdus merula", day(10))

		got, err := ds.GetSpeciesLastDetectionDateBefore(ctx, "Turdus merula", cutoff)
		require.NoError(t, err, "MySQL must not raise a 1064 syntax error")
		assert.Equal(t, "2024-06-10", got)
	})

	t.Run("concatenated label matches via LIKE prefix", func(t *testing.T) {
		// Legacy labels stored as "Scientific_Common" must still match a query
		// for the bare scientific name through the LIKE '<name>_%' arm.
		seedDetection(t, ds, "Strix aluco_lehtopöllö", day(11))

		got, err := ds.GetSpeciesLastDetectionDateBefore(ctx, "Strix aluco", cutoff)
		require.NoError(t, err)
		assert.Equal(t, "2024-06-11", got)
	})

	t.Run("percent in name is escaped to a literal, not a wildcard", func(t *testing.T) {
		// "Ab%cd_Name" is the real match; "AbZZcd_Name" is a decoy that would be
		// matched too if '%' were treated as a LIKE wildcard instead of a literal.
		seedDetection(t, ds, "Ab%cd_Name", day(11))
		seedDetection(t, ds, "AbZZcd_Name", day(13))

		got, err := ds.GetSpeciesLastDetectionDateBefore(ctx, "Ab%cd", cutoff)
		require.NoError(t, err)
		assert.Equal(t, "2024-06-11", got,
			"decoy detection must not be matched: '%%' must be a literal, not a wildcard")
	})

	t.Run("underscore in name is escaped to a literal, not a wildcard", func(t *testing.T) {
		// "Ax_cy_Name" is the real match; "AxXcy_Name" is a decoy that would be
		// matched too if '_' were treated as a single-character LIKE wildcard.
		seedDetection(t, ds, "Ax_cy_Name", day(11))
		seedDetection(t, ds, "AxXcy_Name", day(13))

		got, err := ds.GetSpeciesLastDetectionDateBefore(ctx, "Ax_cy", cutoff)
		require.NoError(t, err)
		assert.Equal(t, "2024-06-11", got,
			"decoy detection must not be matched: '_' must be a literal, not a wildcard")
	})
}
