package analysis

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/privacyrollup"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
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

// TestStartPrivacyRollup_PersistsDiscardRows: a discard recorded on the started
// writer reaches the database through the real repository, with its source and
// hit rows. It catches a missing entity registration or a service that wires the
// wrong store.
func TestStartPrivacyRollup_PersistsDiscardRows(t *testing.T) {
	t.Parallel()

	svc := newPrivacyRollupTestService(t, true)
	svc.startPrivacyRollup()
	require.NotNil(t, svc.privacyRollup)

	svc.privacyRollup.RecordDiscard(privacyrollup.Discard{
		At: time.Now(), SourceURI: "hw:0,0", NodeName: "node", ScientificName: "Parus major", CommonName: "Great Tit",
		Confidence: 0.9, ModelID: "m", ClipBegin: time.Now().Add(-3 * time.Second), ClipEnd: time.Now().Add(6 * time.Second),
		Hits: []privacyrollup.DiscardHit{{Detector: "m", Trigger: "label", Count: 1, MinScore: 0.4, MaxScore: 0.4}},
	})
	require.NoError(t, svc.stopPrivacyRollup(context.WithoutCancel(t.Context())))

	db := svc.dbService.V2Manager().DB()
	var discards []entities.PrivacyFilterDiscard
	require.NoError(t, db.Preload("Hits").Find(&discards).Error)
	require.Len(t, discards, 1)
	require.NotNil(t, discards[0].SourceID)
	assert.Equal(t, "Great Tit", discards[0].CommonName)
	require.Len(t, discards[0].Hits, 1)
	assert.Equal(t, "label", discards[0].Hits[0].TriggerType)
}
