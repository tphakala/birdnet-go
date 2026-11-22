package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// Release strings and variant ids of the synthetic two-release entry the update
// tests use: an entry of release 2.0 whose 1.0 build is a Legacy variant.
const (
	updateEntryID   = "upd-model"
	updateOldID     = "v1"
	updateNewID     = "v2"
	updateOldRel    = "1.0"
	updateNewRel    = "2.0"
	updateModelName = "Update Model"
)

// updateTestEntry is an entry with a live 2.0 variant and a Legacy 1.0 variant
// that names it as successor. Each variant carries its own files so the model
// manager can scan either one.
func updateTestEntry() classifier.CatalogEntry {
	files := func(prefix string) []classifier.CatalogFile {
		return []classifier.CatalogFile{
			apiFile(classifier.RoleModel, prefix+".onnx", apiSizeModel),
			apiFile(classifier.RoleLabels, prefix+".txt", apiSizeLabels),
		}
	}
	return classifier.CatalogEntry{
		ID: updateEntryID, Name: updateModelName, Version: updateNewRel, Category: classifier.CategoryBird,
		HuggingFaceRepo: "t/" + updateEntryID,
		Variants: []classifier.CatalogVariant{
			{ID: updateNewID, Precision: "fp32", Default: true, Files: files("m_v2")},
			{ID: updateOldID, Precision: "fp32", Legacy: true, SupersededBy: updateNewID, Version: updateOldRel, Files: files("m_v1")},
		},
	}
}

func TestUpdateOffers(t *testing.T) {
	t.Parallel()

	withdrawn := updateTestEntry()
	withdrawn.ID, withdrawn.Name = "withdrawn", "Withdrawn"
	withdrawn.Variants[1].Version = "" // same release as its successor
	dangling := updateTestEntry()
	dangling.ID, dangling.Name = "dangling", "Dangling"
	dangling.Variants[1].SupersededBy = "missing"
	replaced := updateTestEntry()
	replaced.ID, replaced.Name = "replaced", "Replaced"
	replaced.SupersededBy = "successor"
	newerBuild := updateTestEntry()
	newerBuild.ID, newerBuild.Name = "newer-build", "Newer build"
	newerBuild.Variants[0].Build = 2
	newerBuild.Variants[1].Version = "" // same release, build 1 -> build 2
	entries := []classifier.CatalogEntry{
		updateTestEntry(), withdrawn, dangling, replaced,
		{ID: "successor", Name: "Successor", Version: "1.0"},
		newerBuild,
	}
	want := updateOffer{
		CatalogID: updateEntryID, ModelName: updateModelName,
		FromVariantID: updateOldID, ToVariantID: updateNewID,
		FromVersion: updateOldRel, ToVersion: updateNewRel,
		FromBuild: 1, ToBuild: 1,
	}
	wantBuild := updateOffer{
		CatalogID: "newer-build", ModelName: "Newer build",
		FromVariantID: updateOldID, ToVariantID: updateNewID,
		FromVersion: updateNewRel, ToVersion: updateNewRel,
		FromBuild: 1, ToBuild: 2,
	}

	tests := []struct {
		name      string
		installed map[string]string
		want      []updateOffer
	}{
		{name: "installed older release gets an offer with both versions", installed: map[string]string{updateEntryID: updateOldID}, want: []updateOffer{want}},
		{name: "installed older build of the same release gets an offer with both builds", installed: map[string]string{"newer-build": updateOldID}, want: []updateOffer{wantBuild}},
		{name: "installed current build gets none", installed: map[string]string{updateEntryID: updateNewID}},
		{name: "withdrawn build of the same release gets none", installed: map[string]string{"withdrawn": updateOldID}},
		{name: "entry with a visible entry-level successor gets none", installed: map[string]string{"replaced": updateOldID}},
		{name: "dangling successor gets none", installed: map[string]string{"dangling": updateOldID}},
		{name: "installed variant missing from the catalog gets none", installed: map[string]string{updateEntryID: "retired"}},
		{name: "not installed gets none", installed: map[string]string{}},
		{name: "empty installed variant gets none", installed: map[string]string{updateEntryID: ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			installedVariant := func(id string) (string, bool) {
				vid, ok := tc.installed[id]
				return vid, ok
			}
			assert.Equal(t, tc.want, updateOffers(entries, installedVariant))
		})
	}
}

func TestUpdateOffersSignature(t *testing.T) {
	t.Parallel()

	a := updateOffer{CatalogID: "a", FromVariantID: "w", ToVariantID: "x"}
	b := updateOffer{CatalogID: "b", FromVariantID: "w", ToVariantID: "y"}
	assert.Equal(t, updateOffersSignature([]updateOffer{a, b}), updateOffersSignature([]updateOffer{b, a}), "the signature ignores order")
	assert.Equal(t, "a:w>x,b:w>y", updateOffersSignature([]updateOffer{b, a}))
	assert.NotEqual(t, updateOffersSignature([]updateOffer{a}), updateOffersSignature([]updateOffer{{CatalogID: "a", FromVariantID: "w", ToVariantID: "z"}}), "a different target is a different offer")
	assert.NotEqual(t, updateOffersSignature([]updateOffer{a}), updateOffersSignature([]updateOffer{{CatalogID: "a", FromVariantID: "v", ToVariantID: "x"}}), "a different installed build is a different offer")
}

func TestNewUpdateNotification(t *testing.T) {
	t.Parallel()

	n := newUpdateNotification([]updateOffer{
		{CatalogID: "a", ModelName: "Model A", FromVersion: "3.0", ToVersion: "3.0.1"},
		{CatalogID: "b", ModelName: "Model B", FromVersion: "1", ToVersion: "2"},
	})
	assert.Equal(t, notification.TypeInfo, n.Type)
	assert.Equal(t, notification.PriorityMedium, n.Priority)
	assert.Equal(t, notification.ComponentClassifier, n.Component)
	assert.Equal(t, notification.MsgModelUpdateTitle, n.TitleKey)
	assert.Equal(t, notification.MsgModelUpdateMessage, n.MessageKey)
	assert.Equal(t, 2, n.TitleParams["count"])
	assert.Equal(t, "Model A (3.0 → 3.0.1), Model B (1 → 2)", n.MessageParams["models"])
	assert.Equal(t, notification.DeliveryTargetBell, n.DeliveryTarget)
	assert.Contains(t, n.Title, "2")
	assert.Contains(t, n.Message, "Model A (3.0 → 3.0.1)")

	one := newUpdateNotification([]updateOffer{{CatalogID: "a", ModelName: "Model A", FromVersion: "3.0", ToVersion: "3.0.1"}})
	assert.Equal(t, updateTitleOne, one.Title)

	// A same-release update reads with the build on both sides, never "2 → 2".
	build := newUpdateNotification([]updateOffer{{CatalogID: "p", ModelName: "Perch v2", FromVersion: "2", ToVersion: "2", FromBuild: 1, ToBuild: 2}})
	assert.Equal(t, "Perch v2 (2-b1 → 2-b2)", build.MessageParams["models"])
}

// TestUpdateNoticeFallbacksMatchEnglishLocale pins that the English fallback texts
// in Go equal the notifications.content.modelUpdate strings in en.json: the
// message with {models} substituted, and the plural title's two forms.
func TestUpdateNoticeFallbacksMatchEnglishLocale(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../../../frontend/static/messages/en.json") //nolint:gosec // repo-relative test input
	require.NoError(t, err)
	var doc struct {
		Notifications struct {
			Content struct {
				ModelUpdate map[string]string `json:"modelUpdate"`
			} `json:"content"`
		} `json:"notifications"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	en := doc.Notifications.Content.ModelUpdate

	assert.Equal(t, updateMessageFormat, strings.ReplaceAll(en["message"], "{models}", "%s"))
	assert.Contains(t, en["title"], "{"+updateTitleOne+"}", "the plural title's one form is the single-model fallback")
	assert.Contains(t, en["title"], "{"+strings.ReplaceAll(updateTitleManyFormat, "%d", "#")+"}", "the plural title's other form is the multi-model fallback")
}

// TestSyncUpdateNotice_Lifecycle covers the latch through applyUpdateNotice with
// the fake sink.
func TestSyncUpdateNotice_Lifecycle(t *testing.T) {
	profile := tfliteOnlyProfile()
	h, notices := newOptimizeTestHandler(t, &profile)
	offers := []updateOffer{{CatalogID: "a", ModelName: "A", ToVariantID: "new", FromVersion: "1", ToVersion: "2"}}

	h.applyUpdateNotice(notices, offers)
	h.applyUpdateNotice(notices, offers)
	created, deleted := notices.counts()
	assert.Equal(t, 1, created, "an unchanged offer set must not raise a second notice")
	assert.Zero(t, deleted)

	h.applyUpdateNotice(notices, nil)
	created, deleted = notices.counts()
	assert.Equal(t, 1, created)
	require.Equal(t, 1, deleted, "an emptied offer set must clear the notice")
	assert.Equal(t, notices.created[0].ID, notices.deleted[0])

	h.applyUpdateNotice(notices, offers)
	created, _ = notices.counts()
	assert.Equal(t, 2, created, "a returning offer must raise a new notice")

	h.applyUpdateNotice(notices, []updateOffer{{CatalogID: "b", ModelName: "B", ToVariantID: "next"}})
	created, deleted = notices.counts()
	assert.Equal(t, 3, created, "a changed offer set must raise a replacement notice")
	assert.Equal(t, 2, deleted, "a changed offer set must delete the notice it replaces")
}

// updateFixture activates the synthetic two-release catalog and returns a handler
// whose model manager scans the models directory the test fills.
type updateFixture struct {
	h        *Handler
	mm       *classifier.ModelManager
	notices  *fakeNotices
	modelDir string
}

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	useCatalog(t, []classifier.CatalogEntry{updateTestEntry()})
	modelDir := t.TempDir()
	mm := classifier.NewModelManager(modelDir, nil, nil)
	h := dependencyHandler(t, mm)
	notices := &fakeNotices{}
	h.notices = notices
	return &updateFixture{h: h, mm: mm, notices: notices, modelDir: modelDir}
}

// setBuild makes exactly one build's files exist on disk (prefix "m_v1" or
// "m_v2") and rescans.
func (f *updateFixture) setBuild(t *testing.T, prefix string) {
	t.Helper()
	for _, p := range []string{"m_v1", "m_v2"} {
		for _, ext := range []string{".onnx", ".txt"} {
			path := filepath.Join(f.modelDir, updateEntryID, p+ext)
			if p == prefix {
				writeFile(t, path)
			} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				require.NoError(t, err)
			}
		}
	}
	f.mm.ScanInstalled()
}

// TestSyncUpdateNotice_FromCatalog drives the whole evaluation against the
// synthetic catalog: raised while the older release is installed, unchanged on a
// repeat evaluation, cleared once the current build replaces it, raised again if
// the older build returns.
func TestSyncUpdateNotice_FromCatalog(t *testing.T) {
	// Not parallel: the active catalog is process-wide.
	f := newUpdateFixture(t)

	f.setBuild(t, "m_v1")
	vid, _ := f.mm.InstalledVariantID(updateEntryID)
	require.Equal(t, updateOldID, vid, "fixture: the older release is the installed build")
	f.h.syncUpdateNotice()
	f.h.syncUpdateNotice()
	created, deleted := f.notices.counts()
	require.Equal(t, 1, created, "an installed older release raises the notice once")
	assert.Zero(t, deleted)
	assert.Equal(t, "Update Model (1.0 → 2.0)", f.notices.created[0].MessageParams["models"])

	f.setBuild(t, "m_v2")
	f.h.syncUpdateNotice()
	created, deleted = f.notices.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, deleted, "the swap to the current build clears the notice")

	f.setBuild(t, "m_v1")
	f.h.syncUpdateNotice()
	created, _ = f.notices.counts()
	assert.Equal(t, 2, created, "a returning older build raises the notice again")
}

func TestFireOptimizeNoticeSync_RunsUpdateSync(t *testing.T) {
	f := newUpdateFixture(t)
	profile := tfliteOnlyProfile()
	f.h.ortProbe = availableORT
	f.h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return profile }
	f.setBuild(t, "m_v1")

	f.h.fireOptimizeNoticeSync()
	f.h.Wait()

	created, _ := f.notices.counts()
	assert.Equal(t, 1, created, "the debounce callback evaluates the update notice")
}

func TestStopOptimizeNoticeSync_StopsUpdateLatch(t *testing.T) {
	f := newUpdateFixture(t)
	f.setBuild(t, "m_v1")

	f.h.StopOptimizeNoticeSync()
	f.h.syncUpdateNotice()

	created, _ := f.notices.counts()
	assert.Zero(t, created, "a stopped handler must not raise the update notice")
}

// TestGetModelCatalog_ReportsUpdate pins the catalog response: an installed older
// release carries update with both versions and the successor variant, the
// installed current build and a model that is not installed carry none.
func TestGetModelCatalog_ReportsUpdate(t *testing.T) {
	f := newUpdateFixture(t)
	h := f.h

	f.setBuild(t, "m_v1")
	entry := findEntry(catalogRequest(t, h), updateEntryID)
	require.NotNil(t, entry)
	require.NotNil(t, entry.Update, "an installed older release reports its update")
	assert.Equal(t, CatalogUpdateResponse{VariantID: updateNewID, FromVersion: updateOldRel, ToVersion: updateNewRel, FromBuild: 1, ToBuild: 1}, *entry.Update)

	f.setBuild(t, "m_v2")
	entry = findEntry(catalogRequest(t, h), updateEntryID)
	require.NotNil(t, entry)
	assert.Nil(t, entry.Update, "the installed current build has no update")

	// A newer build of the same release reports both build numbers, so a swapped
	// or duplicated copy into the response would show.
	sameRelease := updateTestEntry()
	sameRelease.Variants[0].Build = 3
	sameRelease.Variants[1].Version = ""
	sameRelease.Variants[1].Build = 2
	useCatalog(t, []classifier.CatalogEntry{sameRelease})
	f.setBuild(t, "m_v1")
	entry = findEntry(catalogRequest(t, h), updateEntryID)
	require.NotNil(t, entry)
	require.NotNil(t, entry.Update)
	assert.Equal(t, CatalogUpdateResponse{VariantID: updateNewID, FromVersion: updateNewRel, ToVersion: updateNewRel, FromBuild: 2, ToBuild: 3}, *entry.Update)
	useCatalog(t, []classifier.CatalogEntry{updateTestEntry()})

	f.setBuild(t, "")
	entry = findEntry(catalogRequest(t, h), updateEntryID)
	require.NotNil(t, entry)
	assert.False(t, entry.Installed, "fixture: nothing is installed")
	assert.Nil(t, entry.Update, "a model that is not installed has no update")
}

// TestSyncUpdateNotice_NilModelManagerIsNoop uses a catalog that holds an
// installed older release, so an evaluation that reached the model manager
// would dereference it.
func TestSyncUpdateNotice_NilModelManagerIsNoop(t *testing.T) {
	// Not parallel: the active catalog is process-wide.
	f := newUpdateFixture(t)
	f.setBuild(t, "m_v1")
	f.h.ModelManager = nil

	assert.NotPanics(t, f.h.syncUpdateNotice)

	created, deleted := f.notices.counts()
	assert.Zero(t, created)
	assert.Zero(t, deleted)
}

// TestApplyUpdateNotice_FailedCreateIsNotLatched pins that a failed create
// leaves nothing latched, so the next evaluation raises the notice.
func TestApplyUpdateNotice_FailedCreateIsNotLatched(t *testing.T) {
	profile := tfliteOnlyProfile()
	h, notices := newOptimizeTestHandler(t, &profile)
	t.Cleanup(h.StopOptimizeNoticeSync)
	offers := []updateOffer{{CatalogID: "a", ModelName: "A", ToVariantID: "new", FromVersion: "1", ToVersion: "2"}}

	notices.createErr = errors.NewStd("sink unavailable")
	h.applyUpdateNotice(notices, offers)
	created, _ := notices.counts()
	require.Zero(t, created)

	notices.createErr = nil
	h.applyUpdateNotice(notices, offers)
	created, _ = notices.counts()
	assert.Equal(t, 1, created, "a failed create must be retried by the next evaluation")
}

// TestApplyUpdateNotice_FailedCreateRearmsEvaluation verifies the update latch
// is wired to ScheduleOptimizeNoticeSync: a failed create arms the latch retry,
// and the retry callback re-arms the debounced evaluation. The timer is
// observed rather than waited out, as in the superseded notice's equivalent test.
func TestApplyUpdateNotice_FailedCreateRearmsEvaluation(t *testing.T) {
	profile := tfliteOnlyProfile()
	h, notices := newOptimizeTestHandler(t, &profile)
	t.Cleanup(h.StopOptimizeNoticeSync)
	notices.createErr = errors.NewStd("create failed")
	h.update.latch.SetRetryDelay(optimizeRetryTestDelay)

	// Start arms the startup evaluation; stop it so any timer seen afterwards
	// was armed by the update latch retry.
	h.StartOptimizeNoticeSync()
	startup := pendingOptimizeTimer(h)
	require.NotNil(t, startup)
	require.True(t, startup.Stop(), "the startup evaluation must still be pending")

	h.applyUpdateNotice(notices, []updateOffer{{CatalogID: "a", ModelName: "A", ToVariantID: "new", FromVersion: "1", ToVersion: "2"}})

	assert.Eventually(t, func() bool {
		return pendingOptimizeTimer(h) != startup
	}, time.Second, time.Millisecond, "a failed create must re-arm the debounced evaluation through the update latch retry")
	created, _ := notices.counts()
	assert.Zero(t, created, "the failing service records no notice")
}

// TestGetModelCatalog_ReportsUpdateToUnauthenticated pins that update does not
// depend on the hardware-recommendation gate: an unauthenticated request on an
// auth-enabled instance gets no recommendations but still gets update.
func TestGetModelCatalog_ReportsUpdateToUnauthenticated(t *testing.T) {
	f := newUpdateFixture(t)
	stub := &stubAuth{authenticated: false}
	f.h.authService = stub
	f.setBuild(t, "m_v1")

	entry := findEntry(catalogRequest(t, f.h), updateEntryID)
	require.NotNil(t, entry)
	assert.True(t, stub.called, "the auth gate must be consulted")
	assert.Empty(t, entry.RecommendedVariantID, "fixture: recommendations are gated off")
	require.NotNil(t, entry.Update, "update is install state, not a hardware recommendation")
	assert.Equal(t, updateNewID, entry.Update.VariantID)
}
