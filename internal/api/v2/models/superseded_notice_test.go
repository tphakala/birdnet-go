package models

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/notification"
)

func supersededEntry(id, name, successor string) classifier.CatalogEntry {
	return classifier.CatalogEntry{ID: id, Name: name, SupersededBy: successor}
}

func TestSupersededOffers(t *testing.T) {
	t.Parallel()

	installedSet := func(ids ...string) func(string) bool {
		return func(id string) bool { return slices.Contains(ids, id) }
	}
	tests := []struct {
		name      string
		entries   []classifier.CatalogEntry
		installed func(string) bool
		want      []supersededOffer
	}{
		{
			name: "installed entry with a visible successor",
			entries: []classifier.CatalogEntry{
				supersededEntry("old", "Old", "new"),
				{ID: "new", Name: "New"},
			},
			installed: installedSet("old"),
			want:      []supersededOffer{{CatalogID: "old", ModelName: "Old", SuccessorID: "new", SuccessorName: "New"}},
		},
		{
			name: "successor installed too still offers",
			entries: []classifier.CatalogEntry{
				supersededEntry("old", "Old", "new"),
				{ID: "new", Name: "New"},
			},
			installed: installedSet("old", "new"),
			want:      []supersededOffer{{CatalogID: "old", ModelName: "Old", SuccessorID: "new", SuccessorName: "New"}},
		},
		{
			name: "superseded entry not installed",
			entries: []classifier.CatalogEntry{
				supersededEntry("old", "Old", "new"),
				{ID: "new", Name: "New"},
			},
			installed: installedSet("new"),
		},
		{
			name:      "successor missing from the visible catalog",
			entries:   []classifier.CatalogEntry{supersededEntry("old", "Old", "new")},
			installed: installedSet("old"),
		},
		{
			name:      "entry without a successor",
			entries:   []classifier.CatalogEntry{{ID: "plain", Name: "Plain"}},
			installed: installedSet("plain"),
		},
		{
			name: "entries that do not qualify before a qualifying one are skipped",
			entries: []classifier.CatalogEntry{
				{ID: "plain", Name: "Plain"},
				supersededEntry("idle", "Idle", "new"),
				supersededEntry("old", "Old", "new"),
				{ID: "new", Name: "New"},
			},
			installed: installedSet("plain", "old"),
			want:      []supersededOffer{{CatalogID: "old", ModelName: "Old", SuccessorID: "new", SuccessorName: "New"}},
		},
		{
			name: "two superseded entries keep catalog order",
			entries: []classifier.CatalogEntry{
				supersededEntry("b", "B", "nb"),
				supersededEntry("a", "A", "na"),
				{ID: "na", Name: "NA"},
				{ID: "nb", Name: "NB"},
			},
			installed: installedSet("a", "b"),
			want: []supersededOffer{
				{CatalogID: "b", ModelName: "B", SuccessorID: "nb", SuccessorName: "NB"},
				{CatalogID: "a", ModelName: "A", SuccessorID: "na", SuccessorName: "NA"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, supersededOffers(tc.entries, tc.installed))
		})
	}
}

func TestSupersededOffersSignature_OrderIndependent(t *testing.T) {
	t.Parallel()
	a := supersededOffer{CatalogID: "a", SuccessorID: "x"}
	b := supersededOffer{CatalogID: "b", SuccessorID: "y"}
	assert.Equal(t, supersededOffersSignature([]supersededOffer{a, b}), supersededOffersSignature([]supersededOffer{b, a}))
	assert.NotEqual(t,
		supersededOffersSignature([]supersededOffer{a}),
		supersededOffersSignature([]supersededOffer{{CatalogID: "a", SuccessorID: "z"}}),
		"a different successor is a different offer")
	assert.Empty(t, supersededOffersSignature(nil))
}

func TestNewSupersededNotification(t *testing.T) {
	t.Parallel()
	n := newSupersededNotification([]supersededOffer{
		{CatalogID: "a", ModelName: "Old A", SuccessorName: "New A"},
		{CatalogID: "b", ModelName: "Old B", SuccessorName: "New B"},
	})
	assert.Equal(t, notification.TypeInfo, n.Type)
	assert.Equal(t, notification.PriorityMedium, n.Priority)
	assert.Equal(t, notification.ComponentClassifier, n.Component)
	assert.Equal(t, notification.MsgModelSupersededTitle, n.TitleKey)
	assert.Equal(t, notification.MsgModelSupersededMessage, n.MessageKey)
	assert.Equal(t, 2, n.TitleParams["count"])
	assert.Equal(t, "Old A → New A, Old B → New B", n.MessageParams["models"])
	assert.Contains(t, n.Title, "2")
	assert.Contains(t, n.Message, "Old A")

	one := newSupersededNotification([]supersededOffer{{CatalogID: "a", ModelName: "Old A", SuccessorName: "New A"}})
	assert.Contains(t, one.Title, "1 ")
}

// TestSyncSupersededNotice_Lifecycle covers the latch (invariant: one latched
// notice per condition) through applySupersededNotice with the fake sink.
func TestSyncSupersededNotice_Lifecycle(t *testing.T) {
	profile := tfliteOnlyProfile()
	h, notices := newOptimizeTestHandler(t, &profile)
	offers := []supersededOffer{{CatalogID: "old", ModelName: "Old", SuccessorID: "new", SuccessorName: "New"}}

	h.applySupersededNotice(notices, offers)
	h.applySupersededNotice(notices, offers)
	created, deleted := notices.counts()
	assert.Equal(t, 1, created, "an unchanged offer set must not raise a second notice")
	assert.Zero(t, deleted)

	h.applySupersededNotice(notices, nil)
	created, deleted = notices.counts()
	assert.Equal(t, 1, created)
	require.Equal(t, 1, deleted, "an emptied offer set must clear the notice")
	assert.Equal(t, notices.created[0].ID, notices.deleted[0])

	h.applySupersededNotice(notices, offers)
	created, _ = notices.counts()
	assert.Equal(t, 2, created, "a returning offer must raise a new notice")

	swapped := []supersededOffer{{CatalogID: "other", ModelName: "Other", SuccessorID: "next", SuccessorName: "Next"}}
	h.applySupersededNotice(notices, swapped)
	created, deleted = notices.counts()
	assert.Equal(t, 3, created, "a changed offer set must raise a replacement notice")
	assert.Equal(t, 2, deleted, "a changed offer set must delete the notice it replaces")
	assert.Equal(t, "Other → Next", notices.created[2].MessageParams["models"])
}

// supersededFixture activates a synthetic catalog in which old-model is
// superseded by new-model, and returns a handler whose model manager has scanned
// a models directory holding only old-model's files.
type supersededFixture struct {
	h        *Handler
	mm       *classifier.ModelManager
	notices  *fakeNotices
	oldFiles []string
}

const (
	supersededOldID = "old-model"
	supersededNewID = "new-model"
)

func newSupersededFixture(t *testing.T) *supersededFixture {
	t.Helper()
	flat := func(id, name, successor string) classifier.CatalogEntry {
		return classifier.CatalogEntry{
			ID: id, Name: name, Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "t/" + id, SupersededBy: successor,
			Files: []classifier.CatalogFile{
				apiFile(classifier.RoleModel, id+".onnx", apiSizeModel),
				apiFile(classifier.RoleLabels, id+".txt", apiSizeLabels),
			},
		}
	}
	useCatalog(t, []classifier.CatalogEntry{
		flat(supersededOldID, "Old Model", supersededNewID),
		flat(supersededNewID, "New Model", ""),
	})
	modelsDir := t.TempDir()
	files := make([]string, 0, 2)
	for _, name := range []string{supersededOldID + ".onnx", supersededOldID + ".txt"} {
		path := filepath.Join(modelsDir, supersededOldID, name)
		writeFile(t, path)
		files = append(files, path)
	}
	mm := classifier.NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(supersededOldID), "fixture: old model installed")
	core := apitest.NewCore(t)
	core.ModelManager = mm
	h := New(core, nil)
	notices := &fakeNotices{}
	h.notices = notices
	return &supersededFixture{h: h, mm: mm, notices: notices, oldFiles: files}
}

// TestSyncSupersededNotice_FromCatalog drives the whole evaluation against a
// synthetic catalog: the notice is raised while the superseded model is installed
// and clears once it is uninstalled.
func TestSyncSupersededNotice_FromCatalog(t *testing.T) {
	// Not parallel: the active catalog is process-wide.
	f := newSupersededFixture(t)

	f.h.syncSupersededNotice()
	created, deleted := f.notices.counts()
	require.Equal(t, 1, created, "installed superseded model with a visible successor raises the notice")
	assert.Zero(t, deleted)
	assert.Equal(t, "Old Model → New Model", f.notices.created[0].MessageParams["models"])

	for _, p := range f.oldFiles {
		require.NoError(t, os.Remove(p))
	}
	f.mm.ScanInstalled()
	f.h.syncSupersededNotice()
	created, deleted = f.notices.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, deleted, "uninstalling the superseded model clears the notice for good")
}

func TestFireOptimizeNoticeSync_RunsSupersededSync(t *testing.T) {
	f := newSupersededFixture(t)
	profile := tfliteOnlyProfile()
	f.h.ortProbe = availableORT
	f.h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return profile }

	f.h.fireOptimizeNoticeSync()
	f.h.Wait()

	created, _ := f.notices.counts()
	assert.Equal(t, 1, created, "the debounce callback evaluates the superseded notice")
}

func TestStopOptimizeNoticeSync_StopsSupersededLatch(t *testing.T) {
	f := newSupersededFixture(t)

	f.h.StopOptimizeNoticeSync()
	f.h.syncSupersededNotice()

	created, _ := f.notices.counts()
	assert.Zero(t, created, "a stopped handler must not raise the superseded notice")
}

// TestSyncSupersededNotice_NilModelManagerIsNoop uses a catalog that holds a
// superseded entry, so an evaluation that reached the model manager would
// dereference it.
func TestSyncSupersededNotice_NilModelManagerIsNoop(t *testing.T) {
	// Not parallel: the active catalog is process-wide.
	f := newSupersededFixture(t)
	f.h.ModelManager = nil

	assert.NotPanics(t, f.h.syncSupersededNotice)

	created, deleted := f.notices.counts()
	assert.Zero(t, created)
	assert.Zero(t, deleted)
}

// TestApplySupersededNotice_FailedCreateIsNotLatched pins that a failed create
// leaves nothing latched, so the next evaluation raises the notice.
func TestApplySupersededNotice_FailedCreateIsNotLatched(t *testing.T) {
	profile := tfliteOnlyProfile()
	h, notices := newOptimizeTestHandler(t, &profile)
	t.Cleanup(h.StopOptimizeNoticeSync)
	offers := []supersededOffer{{CatalogID: "old", ModelName: "Old", SuccessorID: "new", SuccessorName: "New"}}

	notices.createErr = errors.NewStd("sink unavailable")
	h.applySupersededNotice(notices, offers)
	created, _ := notices.counts()
	require.Zero(t, created)

	notices.createErr = nil
	h.applySupersededNotice(notices, offers)
	created, _ = notices.counts()
	assert.Equal(t, 1, created, "a failed create must be retried by the next evaluation")
}

// TestSyncSupersededNotice_FailedCreateRearmsEvaluation verifies the superseded
// latch is wired to ScheduleOptimizeNoticeSync: a failed create arms the latch
// retry, and the retry callback re-arms the debounced evaluation. The timer is
// observed rather than waited out, as in the optimize notice's equivalent test.
func TestSyncSupersededNotice_FailedCreateRearmsEvaluation(t *testing.T) {
	profile := tfliteOnlyProfile()
	h, notices := newOptimizeTestHandler(t, &profile)
	t.Cleanup(h.StopOptimizeNoticeSync)
	notices.createErr = errors.NewStd("create failed")
	h.superseded.latch.SetRetryDelay(optimizeRetryTestDelay)

	// Start arms the startup evaluation; stop it so any timer seen afterwards
	// was armed by the superseded latch retry.
	h.StartOptimizeNoticeSync()
	startup := pendingOptimizeTimer(h)
	require.NotNil(t, startup)
	require.True(t, startup.Stop(), "the startup evaluation must still be pending")

	h.applySupersededNotice(notices, []supersededOffer{{CatalogID: "old", ModelName: "Old", SuccessorID: "new", SuccessorName: "New"}})

	assert.Eventually(t, func() bool {
		return pendingOptimizeTimer(h) != startup
	}, time.Second, time.Millisecond, "a failed create must re-arm the debounced evaluation through the superseded latch retry")
	created, _ := notices.counts()
	assert.Zero(t, created, "the failing service records no notice")
}
