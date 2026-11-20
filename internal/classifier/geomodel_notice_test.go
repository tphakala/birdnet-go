package classifier

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// coverage builds a ClassifierCoverage row for a notice test.
func coverage(id, name string, covered bool) ClassifierCoverage {
	return ClassifierCoverage{ID: id, Name: name, CoveredByBackend: covered}
}

// TestGeomodelNoticeFor pins which status values raise a notice, which models the text
// names and when the signature changes (a changed signature replaces the notice, an
// unchanged one is a no-op).
func TestGeomodelNoticeFor(t *testing.T) {
	t.Parallel()

	perch := coverage(RegistryIDPerchV2, ModelNamePerchV2, false)
	plus := coverage(RegistryIDBirdNETPlusV3, ModelNameBirdNETPlusV30, false)
	v24 := coverage(RegistryIDBirdNETV24, "BirdNET v2.4", false)

	status := func(issue GeomodelIssue, classifiers ...ClassifierCoverage) *RangeFilterStatusResponse {
		return &RangeFilterStatusResponse{GeomodelIssue: issue, Classifiers: classifiers}
	}

	t.Run("no issue raises nothing", func(t *testing.T) {
		t.Parallel()
		sig, _, models := geomodelNoticeFor(status(GeomodelIssueNone, perch))
		assert.Empty(t, sig)
		assert.Empty(t, models)
	})

	t.Run("not selected raises nothing", func(t *testing.T) {
		t.Parallel()
		sig, _, _ := geomodelNoticeFor(status(GeomodelIssueNotSelected, perch))
		assert.Empty(t, sig, "a deliberate custom range filter is not a notice")
	})

	t.Run("not installed names the uncovered model", func(t *testing.T) {
		t.Parallel()
		sig, issue, models := geomodelNoticeFor(status(GeomodelIssueNotInstalled, perch))
		assert.NotEmpty(t, sig)
		assert.Equal(t, GeomodelIssueNotInstalled, issue)
		assert.Equal(t, []string{ModelNamePerchV2}, models)
	})

	t.Run("BirdNET v2.4 is never named", func(t *testing.T) {
		t.Parallel()
		_, _, models := geomodelNoticeFor(status(GeomodelIssueNotInstalled, v24, plus))
		assert.Equal(t, []string{ModelNameBirdNETPlusV30}, models, "v2.4 does not need the geomodel")
	})

	t.Run("a covered model is not named", func(t *testing.T) {
		t.Parallel()
		covered := coverage(RegistryIDPerchV2, ModelNamePerchV2, true)
		_, _, models := geomodelNoticeFor(status(GeomodelIssueLoadFailed, covered, plus))
		assert.Equal(t, []string{ModelNameBirdNETPlusV30}, models)
	})

	t.Run("no uncovered geomodel model raises nothing", func(t *testing.T) {
		t.Parallel()
		sig, _, _ := geomodelNoticeFor(status(GeomodelIssueNotInstalled, v24))
		assert.Empty(t, sig, "a notice must name at least one model")
	})

	t.Run("the signature follows the model set", func(t *testing.T) {
		t.Parallel()
		one, _, _ := geomodelNoticeFor(status(GeomodelIssueNotInstalled, perch))
		two, _, _ := geomodelNoticeFor(status(GeomodelIssueNotInstalled, plus, perch))
		assert.NotEqual(t, one, two)
	})

	t.Run("the signature follows the issue kind", func(t *testing.T) {
		t.Parallel()
		missing, _, _ := geomodelNoticeFor(status(GeomodelIssueNotInstalled, perch))
		failed, _, _ := geomodelNoticeFor(status(GeomodelIssueLoadFailed, perch))
		assert.NotEqual(t, missing, failed, "the remedy text differs per issue")
	})

	t.Run("an unchanged status keeps its signature", func(t *testing.T) {
		t.Parallel()
		first, _, _ := geomodelNoticeFor(status(GeomodelIssueNotInstalled, plus, perch))
		second, _, _ := geomodelNoticeFor(status(GeomodelIssueNotInstalled, plus, perch))
		assert.Equal(t, first, second)
	})
}

// TestSyncGeomodelNotice_FollowsRangeFilterState walks one orchestrator through model
// load, replacement, a failed range filter reload, the geomodel serving and teardown.
// The notice is raised by the range filter reload hook: the steps that load models or
// reload the range filter never call syncGeomodelNotice themselves. The unchanged-state
// step and the injected-state steps near the end do call it directly.
func TestSyncGeomodelNotice_FollowsRangeFilterState(t *testing.T) {
	// Not parallel: uses the process-global notification service, settings snapshot and
	// model loaders.
	svc := setupTestNotification(t)
	isolateTestConfig(t)

	modelsDir := t.TempDir()
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{}
	settings.Models.Directory = modelsDir
	settings.BirdNET.RangeFilter.Model = ""
	settings.BirdNET.RangeFilter.ModelPath = ""
	// StoreSettings publishes a snapshot, so every mutation below is followed by publish.
	publish := func() { conftest.SetTestSettings(settings) }
	publish()
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	labels := []string{"Cyanistes caeruleus_Eurasian Blue Tit"}
	for _, id := range []string{RegistryIDPerchV2, RegistryIDBirdNETPlusV3} {
		overrideModelLoader(t, id, func(o *Orchestrator, _ int) error {
			o.models[id] = &modelEntry{instance: &mockModelInstance{id: id, labels: labels, numSpecies: len(labels)}}
			return nil
		})
	}

	geomodelNotices := func() []*notification.Notification {
		notes, err := svc.List(nil)
		require.NoError(t, err)
		var out []*notification.Notification
		for _, n := range notes {
			if n.TitleKey == notification.MsgGeomodelNotInstalledTitle || n.TitleKey == notification.MsgGeomodelLoadFailedTitle {
				out = append(out, n)
			}
		}
		return out
	}

	o, err := NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(func() { o.Delete() })
	assert.Empty(t, geomodelNotices(), "no model is loaded at N=0")

	// Perch loads without the geomodel: the reload hook raises the notice.
	settings.Models.Enabled = []string{conf.ModelIDPerchV2}
	publish()
	_, _, err = o.ReconcileEnabledModels()
	require.NoError(t, err)
	notes := geomodelNotices()
	require.Len(t, notes, 1)
	first := notes[0]
	assert.Equal(t, notification.MsgGeomodelNotInstalledTitle, first.TitleKey)
	assert.Equal(t, notification.MsgGeomodelNotInstalledMessage, first.MessageKey)
	assert.Equal(t, ModelNamePerchV2, first.MessageParams["models"])
	assert.Equal(t, notification.TypeWarning, first.Type)
	assert.Equal(t, notification.PriorityMedium, first.Priority)
	assert.Equal(t, notification.DeliveryTargetBell, first.DeliveryTarget)
	assert.Contains(t, first.Message, "Settings > Analysis > Models")
	assert.Empty(t, first.Metadata, "the bell renders metadata as raw lines")

	// The same state again is a no-op, both through a reconcile and through a direct sync.
	_, _, err = o.ReconcileEnabledModels()
	require.NoError(t, err)
	o.syncGeomodelNotice()
	notes = geomodelNotices()
	require.Len(t, notes, 1)
	assert.Equal(t, first.ID, notes[0].ID, "an unchanged state keeps the same notice")

	// A second model that needs the geomodel replaces the notice and is named in it.
	settings.Models.Enabled = []string{conf.ModelIDPerchV2, conf.ModelIDBirdNETPlusV3}
	publish()
	_, _, err = o.ReconcileEnabledModels()
	require.NoError(t, err)
	notes = geomodelNotices()
	require.Len(t, notes, 1)
	assert.NotEqual(t, first.ID, notes[0].ID, "a changed model set replaces the notice")
	assert.Equal(t, ModelNameBirdNETPlusV30+", "+ModelNamePerchV2, notes[0].MessageParams["models"])

	// A failed range filter reload (the geomodel is selected but its files are missing)
	// returns before anything else in ReloadRangeFilter runs, so only the reload hook can
	// switch the notice to the load-failure text.
	settings.BirdNET.RangeFilter.Model = conf.RangeFilterModelV3
	publish()
	require.Error(t, o.ReloadRangeFilter())
	notes = geomodelNotices()
	require.Len(t, notes, 1)
	assert.Equal(t, notification.MsgGeomodelLoadFailedTitle, notes[0].TitleKey)
	assert.Equal(t, notification.MsgGeomodelLoadFailedMessage, notes[0].MessageKey)
	assert.Contains(t, notes[0].Message, "could not be loaded")
	assert.Contains(t, notes[0].Message, "Settings > Analysis > Models")

	// The geomodel serves both models: the notice clears. A real geomodel build needs ONNX
	// Runtime and the model file, so the serving state is injected; the reload trigger is
	// pinned by the steps above.
	settings.BirdNET.RangeFilter.Model = ""
	publish()
	writeSharedGeomodel(t, modelsDir)
	servingState := func(generation uint64) *rangeFilterState {
		return &rangeFilterState{
			backend: &fakeUniversalRangeFilter{}, kind: rfKindGeomodelV3, generation: generation,
			participants: []participantLabels{
				{id: RegistryIDBirdNETPlusV3, labels: labels},
				{id: RegistryIDPerchV2, labels: labels},
			},
		}
	}
	o.rangeFilter.state.Store(servingState(10))
	o.syncGeomodelNotice()
	assert.Empty(t, geomodelNotices(), "the notice clears once the geomodel serves both models")
	assert.Empty(t, o.geomodelNotice.ID(), "the latch is cleared")

	// The geomodel goes away again: the notice is back, and teardown removes it.
	o.rangeFilter.state.Store(&rangeFilterState{kind: rfKindNone, generation: 11})
	settings.BirdNET.RangeFilter.Model = conf.RangeFilterModelV3
	publish()
	o.syncGeomodelNotice()
	require.Len(t, geomodelNotices(), 1)

	o.Delete()
	assert.Empty(t, geomodelNotices(), "Delete clears the notice so a rebuilt orchestrator does not add a second one")
}

// TestGeomodelNoticeFallbacksMatchEnglishLocale pins that the English fallback texts in Go
// equal the notifications.content.geomodel strings in en.json, with %s for {models}.
func TestGeomodelNoticeFallbacksMatchEnglishLocale(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../frontend/static/messages/en.json") //nolint:gosec // repo-relative test input
	require.NoError(t, err)
	var doc struct {
		Notifications struct {
			Content struct {
				Geomodel map[string]string `json:"geomodel"`
			} `json:"content"`
		} `json:"notifications"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	en := doc.Notifications.Content.Geomodel

	assert.Equal(t, geomodelNotInstalledTitle, en["notInstalledTitle"])
	assert.Equal(t, geomodelLoadFailedTitle, en["loadFailedTitle"])
	assert.Equal(t, geomodelNotInstalledMessage, strings.ReplaceAll(en["notInstalledMessage"], "{models}", "%s"))
	assert.Equal(t, geomodelLoadFailedMessage, strings.ReplaceAll(en["loadFailedMessage"], "{models}", "%s"))
}

// TestScanInstalled_RaisesGeomodelNoticeWhenServiceStartsLate pins the ScanInstalled sync:
// an orchestrator built before the notification service exists latches nothing, so the
// startup scan is what raises the notice. The range filter paths stay empty so the scan
// itself does not reload the range filter, which would raise the notice through the reload
// hook without the ScanInstalled call.
func TestScanInstalled_RaisesGeomodelNoticeWhenServiceStartsLate(t *testing.T) {
	// Not parallel: uses the process-global notification service, settings snapshot and
	// model loaders.
	isolateTestConfig(t)
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)

	modelsDir := t.TempDir()
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{conf.ModelIDPerchV2}
	settings.Models.Directory = modelsDir
	settings.Models.AutoEnableMigrated = true
	settings.BirdNET.RangeFilter.Model = ""
	settings.BirdNET.RangeFilter.ModelPath = ""
	settings.BirdNET.RangeFilter.LabelsPath = ""
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	labels := []string{"Cyanistes caeruleus_Eurasian Blue Tit"}
	overrideModelLoader(t, RegistryIDPerchV2, func(o *Orchestrator, _ int) error {
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &mockModelInstance{id: RegistryIDPerchV2, labels: labels, numSpecies: len(labels)}}
		return nil
	})

	o, err := NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(func() { o.Delete() })
	require.True(t, o.IsModelLoaded(RegistryIDPerchV2))

	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)
	notes, err := svc.List(nil)
	require.NoError(t, err)
	require.Empty(t, notes, "nothing was raised while the service did not exist")

	NewModelManager(modelsDir, o, settings).ScanInstalled()

	notes, err = svc.List(nil)
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, notification.MsgGeomodelNotInstalledTitle, notes[0].TitleKey)
}
