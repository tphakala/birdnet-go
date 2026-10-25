package analysis

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
)

func newPrivacyRollupTestService(t *testing.T, v2Only bool) *APIServerService {
	t.Helper()
	mgr, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{DirectPath: filepath.Join(t.TempDir(), "rollup.db")})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })
	return &APIServerService{dbService: &DatabaseService{v2Manager: mgr, v2OnlyMode: v2Only}, proc: &processor.Processor{}}
}

// TestStartPrivacyRollup_OnlyInV2OnlyMode: a v2 manager alone (the migration
// window) must not start a writer; only the v2-only mode does.
func TestStartPrivacyRollup_OnlyInV2OnlyMode(t *testing.T) {
	t.Parallel()

	migration := newPrivacyRollupTestService(t, false)
	migration.startPrivacyRollup()
	assert.Nil(t, migration.privacyRollup, "a v2 manager outside v2-only mode gets no writer")

	v2Only := newPrivacyRollupTestService(t, true)
	v2Only.startPrivacyRollup()
	require.NotNil(t, v2Only.privacyRollup)
	assert.Same(t, v2Only.privacyRollup, v2Only.proc.PrivacyRollup(), "the processor records into the started writer")
	// t.Context() is already canceled when cleanups run, so Stop would not wait;
	// WithoutCancel keeps the test context values but not its cancellation.
	t.Cleanup(func() { require.NoError(t, v2Only.stopPrivacyRollup(context.WithoutCancel(t.Context()))) })

	noDB := &APIServerService{}
	noDB.startPrivacyRollup()
	assert.Nil(t, noDB.privacyRollup)
}
