package models

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// fakeNotices records the optimize notices created and deleted, standing in for
// the process-wide notification service.
type fakeNotices struct {
	mu        sync.Mutex
	created   []*notification.Notification
	deleted   []string
	createErr error
	deleteErr error
}

func (f *fakeNotices) CreateWithMetadata(n *notification.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, n)
	return nil
}

func (f *fakeNotices) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeNotices) counts() (created, deleted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created), len(f.deleted)
}

// aarch64LowRAMONNXProfile is a Raspberry Pi class host: arm64, 1 GB RAM, ONNX
// Runtime available. The recommender picks the INT8 DFT build of BirdNET v2.4
// there, so a host still on the built-in baseline has an optimize offer.
func aarch64LowRAMONNXProfile() hwprofile.Profile {
	return hwprofile.Profile{
		Arch:          "arm64",
		TotalRAMBytes: 1 * 1024 * 1024 * 1024,
		Backends:      hwprofile.Backends{ONNX: hwprofile.BackendStatus{Available: true}},
	}
}

// tfliteOnlyProfile is an amd64 host with only TFLite linked, where the built-in
// v2.4 baseline is already the recommended variant (no offer).
func tfliteOnlyProfile() hwprofile.Profile {
	return hwprofile.Profile{
		Arch:          "amd64",
		TotalRAMBytes: 16 * 1024 * 1024 * 1024,
		Backends:      hwprofile.Backends{TFLite: hwprofile.BackendStatus{Available: true}},
	}
}

// newOptimizeTestHandler builds a handler whose ModelManager has scanned an empty
// models directory (so only the permanent BirdNET v2.4 entry is installed, on its
// built-in baseline), with a fake notice sink and a settable host profile.
func newOptimizeTestHandler(t *testing.T, profile *hwprofile.Profile) (*Handler, *fakeNotices) {
	t.Helper()
	h, notices, _ := newCountingOptimizeTestHandler(t, profile)
	return h, notices
}

// newCountingOptimizeTestHandler is newOptimizeTestHandler plus a counter of
// offer evaluations: each evaluation probes the host profile exactly once, so
// the profile seam counts them even when the latch turns a repeat into a no-op.
func newCountingOptimizeTestHandler(t *testing.T, profile *hwprofile.Profile) (*Handler, *fakeNotices, *atomic.Int32) {
	t.Helper()
	core := apitest.NewCore(t)
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)
	mm.ScanInstalled()
	core.ModelManager = mm
	h := New(core, nil)
	evaluations := &atomic.Int32{}
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile {
		evaluations.Add(1)
		return *profile
	}
	notices := &fakeNotices{}
	h.notices = notices
	return h, notices, evaluations
}

func variantEntry(id, name string, variantIDs ...string) classifier.CatalogEntry {
	e := classifier.CatalogEntry{ID: id, Name: name}
	for _, v := range variantIDs {
		e.Variants = append(e.Variants, classifier.CatalogVariant{ID: v})
	}
	return e
}

func TestOptimizeOffers(t *testing.T) {
	t.Parallel()

	entries := []classifier.CatalogEntry{
		variantEntry("a", "Model A", "base", "fast"),
		variantEntry("b", "Model B", "base", "fast"),
		{ID: "flat", Name: "Flat model"},
	}
	compatible := func(ids ...string) map[string]recommend.Recommendation {
		m := make(map[string]recommend.Recommendation, len(ids))
		for _, id := range ids {
			m[id] = recommend.Recommendation{VariantID: id, Compatible: true}
		}
		return m
	}

	tests := []struct {
		name        string
		installed   map[string]string
		byVariant   map[string]map[string]recommend.Recommendation
		recommended map[string]string
		want        []optimizeOffer
	}{
		{
			name:        "installed variant differs from a compatible recommendation",
			installed:   map[string]string{"a": "base"},
			byVariant:   map[string]map[string]recommend.Recommendation{"a": compatible("base", "fast")},
			recommended: map[string]string{"a": "fast"},
			want:        []optimizeOffer{{CatalogID: "a", ModelName: "Model A", FromVariantID: "base", ToVariantID: "fast"}},
		},
		{
			name:        "installed variant is already the recommendation",
			installed:   map[string]string{"a": "fast"},
			byVariant:   map[string]map[string]recommend.Recommendation{"a": compatible("base", "fast")},
			recommended: map[string]string{"a": "fast"},
		},
		{
			name:        "model not installed",
			installed:   map[string]string{},
			byVariant:   map[string]map[string]recommend.Recommendation{"a": compatible("base", "fast")},
			recommended: map[string]string{"a": "fast"},
		},
		{
			name:      "recommended variant incompatible",
			installed: map[string]string{"a": "base"},
			byVariant: map[string]map[string]recommend.Recommendation{"a": {
				"fast": {VariantID: "fast", Compatible: false},
			}},
			recommended: map[string]string{"a": "fast"},
		},
		{
			name:        "no recommendation for the entry",
			installed:   map[string]string{"a": "base"},
			byVariant:   map[string]map[string]recommend.Recommendation{"a": compatible("base", "fast")},
			recommended: map[string]string{},
		},
		{
			name:        "flat entry is never offered",
			installed:   map[string]string{"flat": ""},
			recommended: map[string]string{"flat": "x"},
		},
		{
			name:        "installed variant dropped from the catalog still offers",
			installed:   map[string]string{"b": "retired"},
			byVariant:   map[string]map[string]recommend.Recommendation{"b": compatible("fast")},
			recommended: map[string]string{"b": "fast"},
			want:        []optimizeOffer{{CatalogID: "b", ModelName: "Model B", FromVariantID: "retired", ToVariantID: "fast"}},
		},
		{
			name:      "several offers keep catalog order",
			installed: map[string]string{"b": "base", "a": "base"},
			byVariant: map[string]map[string]recommend.Recommendation{
				"a": compatible("fast"), "b": compatible("fast"),
			},
			recommended: map[string]string{"a": "fast", "b": "fast"},
			want: []optimizeOffer{
				{CatalogID: "a", ModelName: "Model A", FromVariantID: "base", ToVariantID: "fast"},
				{CatalogID: "b", ModelName: "Model B", FromVariantID: "base", ToVariantID: "fast"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, optimizeOffers(entries, tc.installed, tc.byVariant, tc.recommended))
		})
	}
}

// TestEnsureHostProbed_BeforeLiveRanking pins that the optimize evaluation runs
// the out-of-process OpenVINO probe before ranking with the live host profile,
// that the install gate never waits on it (it runs on the request path), and
// that nothing probes under the test profile seam.
func TestEnsureHostProbed_BeforeLiveRanking(t *testing.T) {
	core := apitest.NewCore(t)
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)
	mm.ScanInstalled()
	core.ModelManager = mm
	h := New(core, nil)
	var probes atomic.Int32
	h.ensureOVProbe = func() { probes.Add(1) }

	entry, ok := classifier.GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok)

	// Live host profile (no seam).
	h.currentOptimizeOffers()
	assert.Equal(t, int32(1), probes.Load(), "the optimize evaluation probes before ranking")
	h.requestedVariantCompatibility(&entry, "", inference.ORTStatus{})
	assert.Equal(t, int32(1), probes.Load(), "the install gate must not wait on the probe")

	// Synthetic profile: nothing to probe.
	profile := tfliteOnlyProfile()
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return profile }
	h.currentOptimizeOffers()
	assert.Equal(t, int32(1), probes.Load(), "the test profile seam never probes")
}

func TestWithoutCustomPrimaryOffer(t *testing.T) {
	t.Parallel()

	v24, ok := classifier.GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok)
	builtinID := ""
	for i := range v24.Variants {
		if v24.Variants[i].BuiltIn {
			builtinID = v24.Variants[i].ID
		}
	}
	require.NotEmpty(t, builtinID, "the permanent v2.4 entry carries a BuiltIn baseline")
	other := variantEntry("perch-v2", "Perch v2", builtinID, "fast")
	other.Variants[0].BuiltIn = true // only the permanent entry's baseline may be skipped
	entries := []classifier.CatalogEntry{v24, other}

	fromBuiltin := optimizeOffer{CatalogID: v24.ID, FromVariantID: builtinID, ToVariantID: "fp32-dfttrunc"}
	fromDFT := optimizeOffer{CatalogID: v24.ID, FromVariantID: "int8-arm-dfttrunc", ToVariantID: "fp32-dfttrunc"}
	otherModel := optimizeOffer{CatalogID: other.ID, FromVariantID: builtinID, ToVariantID: "fast"}

	tests := []struct {
		name       string
		configured string
		in         []optimizeOffer
		want       []optimizeOffer
	}{
		{"default install keeps the baseline offer", "", []optimizeOffer{fromBuiltin}, []optimizeOffer{fromBuiltin}},
		{"custom primary file drops the baseline offer", "/data/models/my-birdnet.tflite", []optimizeOffer{fromBuiltin}, []optimizeOffer{}},
		{"a gallery build on disk keeps its offer", "/data/models/birdnet-v2.4/x.onnx", []optimizeOffer{fromDFT}, []optimizeOffer{fromDFT}},
		{"another model's offer is untouched", "/data/models/my-birdnet.tflite", []optimizeOffer{otherModel}, []optimizeOffer{otherModel}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := slices.Clone(tc.in)
			assert.Equal(t, tc.want, withoutCustomPrimaryOffer(in, entries, tc.configured))
		})
	}
}

// TestSyncOptimizeNotice_CustomPrimaryModelGetsNoNotice pins that a user whose
// configured BirdNET v2.4 model is their own file is not told to optimize it away,
// on the same host where a default install gets the notice.
func TestSyncOptimizeNotice_CustomPrimaryModelGetsNoNotice(t *testing.T) {
	core := apitest.NewCore(t, apitest.WithSettingsFunc(func(s *conf.Settings) {
		s.BirdNET.ModelPath = "/data/models/my-birdnet.tflite"
	}))
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)
	mm.ScanInstalled()
	core.ModelManager = mm
	h := New(core, nil)
	profile := aarch64LowRAMONNXProfile()
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return profile }
	notices := &fakeNotices{}
	h.notices = notices

	h.syncOptimizeNotice()

	created, _ := notices.counts()
	assert.Zero(t, created)
}

func TestOptimizeOffersSignature_OrderIndependent(t *testing.T) {
	t.Parallel()
	a := optimizeOffer{CatalogID: "a", ToVariantID: "fast"}
	b := optimizeOffer{CatalogID: "b", ToVariantID: "fast"}
	assert.Equal(t, optimizeOffersSignature([]optimizeOffer{a, b}), optimizeOffersSignature([]optimizeOffer{b, a}))
	assert.NotEqual(t, optimizeOffersSignature([]optimizeOffer{a}), optimizeOffersSignature([]optimizeOffer{a, b}))
	assert.Empty(t, optimizeOffersSignature(nil))
	// The same model recommended onto a different build is a different offer, so
	// a notice the user deleted is raised again when the recommendation moves.
	assert.NotEqual(t,
		optimizeOffersSignature([]optimizeOffer{{CatalogID: "a", ToVariantID: "fast"}}),
		optimizeOffersSignature([]optimizeOffer{{CatalogID: "a", ToVariantID: "fast-int8"}}))
}

// TestApplyOptimizeNotice_ReplacesChangedOfferSet pins the replace path: when
// one non-empty offer set changes to another, the old notice is deleted and a
// single new one raised, so the bell never shows two optimize notices.
func TestApplyOptimizeNotice_ReplacesChangedOfferSet(t *testing.T) {
	h := New(apitest.NewCore(t, apitest.WithoutSettingsPublish()), nil)
	notices := &fakeNotices{}
	a := optimizeOffer{CatalogID: "a", ModelName: "Model A", ToVariantID: "fast"}
	b := optimizeOffer{CatalogID: "b", ModelName: "Model B", ToVariantID: "fast"}

	h.applyOptimizeNotice(notices, []optimizeOffer{a})
	h.applyOptimizeNotice(notices, []optimizeOffer{a, b})

	require.Len(t, notices.created, 2)
	assert.Equal(t, []string{notices.created[0].ID}, notices.deleted, "the old notice is deleted exactly once")
	assert.Equal(t, notices.created[1].ID, h.optimize.latch.ID(), "the latch holds the new notice")
	assert.Equal(t, 2, notices.created[1].TitleParams["count"])
	assert.Equal(t, "2 models have better builds for this system", notices.created[1].Title,
		"the plural English fallback matches en.json")
	assert.Equal(t, "Model A, Model B", notices.created[1].MessageParams["models"])
}

// TestApplyOptimizeNotice_DeleteFailureKeepsLatch pins that a failed delete of
// the old notice aborts the reconciliation with the latch intact, so no second
// notice is raised beside the old one and the next trigger retries.
func TestApplyOptimizeNotice_DeleteFailureKeepsLatch(t *testing.T) {
	h := New(apitest.NewCore(t, apitest.WithoutSettingsPublish()), nil)
	notices := &fakeNotices{}
	a := optimizeOffer{CatalogID: "a", ModelName: "Model A", ToVariantID: "fast"}
	b := optimizeOffer{CatalogID: "b", ModelName: "Model B", ToVariantID: "fast"}

	h.applyOptimizeNotice(notices, []optimizeOffer{a})
	require.Len(t, notices.created, 1)
	first := notices.created[0].ID

	notices.deleteErr = errors.NewStd("store unavailable")
	h.applyOptimizeNotice(notices, []optimizeOffer{a, b})
	h.applyOptimizeNotice(notices, nil)
	assert.Len(t, notices.created, 1, "no replacement is raised while the old notice cannot be deleted")
	assert.Equal(t, first, h.optimize.latch.ID(), "the latch still holds the old notice")

	notices.deleteErr = nil
	h.applyOptimizeNotice(notices, []optimizeOffer{a, b})
	assert.Equal(t, []string{first}, notices.deleted, "the retry deletes the old notice")
	require.Len(t, notices.created, 2)
	assert.Equal(t, notices.created[1].ID, h.optimize.latch.ID())
}

// TestSetNotificationService_RoutesNotice pins that the facade-injected
// notification service receives the notice instead of the process-wide one,
// and that a nil service leaves the fallback in place. Not parallel: it resets
// the process-wide notification service.
func TestSetNotificationService_RoutesNotice(t *testing.T) {
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)

	profile := aarch64LowRAMONNXProfile()
	h, _ := newOptimizeTestHandler(t, &profile)
	h.notices = nil

	h.SetNotificationService(nil)
	assert.Nil(t, h.noticeSvc(), "a nil service keeps the (uninitialized) process-wide fallback")

	injected := notification.NewService(notification.DefaultServiceConfig())
	t.Cleanup(injected.Stop)
	h.SetNotificationService(injected)
	h.syncOptimizeNotice()

	stored, err := injected.List(&notification.FilterOptions{Types: []notification.Type{notification.TypeInfo}})
	require.NoError(t, err)
	require.Len(t, stored, 1, "the notice lands in the injected service")
	assert.Equal(t, notification.MsgModelOptimizeTitle, stored[0].TitleKey)
}

// TestSyncOptimizeNotice_Concurrent runs overlapping evaluations under -race:
// optimize.mu serializes them, so the latch ends consistent with one notice.
func TestSyncOptimizeNotice_Concurrent(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices := newOptimizeTestHandler(t, &profile)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(h.syncOptimizeNotice)
	}
	wg.Wait()

	created, deleted := notices.counts()
	assert.Equal(t, 1, created, "concurrent evaluations of one offer set raise one notice")
	assert.Zero(t, deleted)
}

// TestSyncOptimizeNotice_RaisesForBuiltinOnRecommendedHost is a #4423-style
// scenario: a Raspberry Pi class host still on the built-in BirdNET v2.4 baseline gets one
// bell notice naming the model, with translation keys and bell-only delivery.
func TestSyncOptimizeNotice_RaisesForBuiltinOnRecommendedHost(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices := newOptimizeTestHandler(t, &profile)

	h.syncOptimizeNotice()

	require.Len(t, notices.created, 1)
	n := notices.created[0]
	assert.Equal(t, notification.TypeInfo, n.Type)
	assert.Equal(t, notification.DeliveryTargetBell, n.DeliveryTarget)
	assert.Equal(t, notification.ComponentClassifier, n.Component)
	assert.Equal(t, notification.MsgModelOptimizeTitle, n.TitleKey)
	assert.Equal(t, notification.MsgModelOptimizeMessage, n.MessageKey)
	assert.Equal(t, 1, n.TitleParams["count"])
	v24, ok := classifier.GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok)
	assert.Equal(t, v24.Name, n.MessageParams["models"], "the notice names exactly the model with the offer")
	assert.Equal(t, "1 model has a better build for this system", n.Title)
	assert.Equal(t,
		"A build better matched to this system's hardware or location is available for: "+v24.Name+". Open Settings > Analysis > Models and choose Optimize to switch.",
		n.Message, "the English fallback matches en.json")
	assert.Empty(t, n.Metadata, "no metadata: the bell renders every scalar metadata key as a raw context line")
}

// TestSyncOptimizeNotice_Lifecycle covers the latch: an unchanged offer set is a
// no-op, an emptied set clears the notice, and a returning set raises a new one.
func TestSyncOptimizeNotice_Lifecycle(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices := newOptimizeTestHandler(t, &profile)

	h.syncOptimizeNotice()
	h.syncOptimizeNotice()
	created, deleted := notices.counts()
	assert.Equal(t, 1, created, "an unchanged offer set must not raise a second notice")
	assert.Zero(t, deleted)

	// The host now recommends the installed baseline: the offer is gone.
	profile = tfliteOnlyProfile()
	h.syncOptimizeNotice()
	created, deleted = notices.counts()
	assert.Equal(t, 1, created)
	require.Equal(t, 1, deleted, "an emptied offer set must clear the notice")
	assert.Equal(t, notices.created[0].ID, notices.deleted[0])

	// Nothing outstanding and still no offer: no-op.
	h.syncOptimizeNotice()
	created, deleted = notices.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, deleted)

	// The offer returns: a fresh notice is raised.
	profile = aarch64LowRAMONNXProfile()
	h.syncOptimizeNotice()
	created, _ = notices.counts()
	assert.Equal(t, 2, created)
}

func TestSyncOptimizeNotice_NoOfferNoNotice(t *testing.T) {
	profile := tfliteOnlyProfile()
	h, notices := newOptimizeTestHandler(t, &profile)

	h.syncOptimizeNotice()

	created, deleted := notices.counts()
	assert.Zero(t, created)
	assert.Zero(t, deleted)
}

// TestSyncOptimizeNotice_CreateFailureRetries verifies a failed create latches
// nothing, so the next evaluation with the same offers raises the notice.
func TestSyncOptimizeNotice_CreateFailureRetries(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices := newOptimizeTestHandler(t, &profile)

	notices.createErr = errors.NewStd("store full")
	h.syncOptimizeNotice()
	created, _ := notices.counts()
	require.Zero(t, created)

	notices.createErr = nil
	h.syncOptimizeNotice()
	created, _ = notices.counts()
	assert.Equal(t, 1, created, "a failed create must be retried on the next sync")
}

func TestSyncOptimizeNotice_NilModelManagerIsNoop(t *testing.T) {
	// Not parallel: apitest.NewCore publishes process-wide settings.
	h := New(apitest.NewCore(t), nil)
	notices := &fakeNotices{}
	h.notices = notices

	h.syncOptimizeNotice()
	h.StartOptimizeNoticeSync()
	h.ScheduleOptimizeNoticeSync()

	created, deleted := notices.counts()
	assert.Zero(t, created)
	assert.Zero(t, deleted)
	h.optimize.timerMu.Lock()
	defer h.optimize.timerMu.Unlock()
	assert.Nil(t, h.optimize.timer, "no evaluation is scheduled without a ModelManager")
}

// pendingOptimizeTimer returns the currently armed debounce timer, or nil.
func pendingOptimizeTimer(h *Handler) *time.Timer {
	h.optimize.timerMu.Lock()
	defer h.optimize.timerMu.Unlock()
	return h.optimize.timer
}

// TestScheduleOptimizeNoticeSync_DebouncesAndStops verifies a burst of schedules
// leaves exactly one pending evaluation (each schedule cancels the one before
// it), that the pending callback runs exactly one evaluation, and that Stop
// cancels the pending timer and ignores later schedules. It inspects the timers
// directly rather than waiting out the debounce: the Core the handler is built
// on runs background goroutines, so it cannot live in a synctest bubble.
func TestScheduleOptimizeNoticeSync_DebouncesAndStops(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices, evaluations := newCountingOptimizeTestHandler(t, &profile)
	t.Cleanup(h.StopOptimizeNoticeSync)

	const burst = 4
	h.StartOptimizeNoticeSync()
	timers := make([]*time.Timer, 0, burst+1)
	timers = append(timers, pendingOptimizeTimer(h))
	for range burst {
		h.ScheduleOptimizeNoticeSync()
		timers = append(timers, pendingOptimizeTimer(h))
	}
	live := timers[len(timers)-1]
	require.NotNil(t, live)
	for i, old := range timers[:len(timers)-1] {
		require.NotSame(t, live, old)
		assert.False(t, old.Stop(), "schedule %d must have cancelled the evaluation pending before it", i)
	}

	// The live timer is still pending; stop it so only the manual fire below
	// runs, then check that callback runs exactly one evaluation.
	require.True(t, live.Stop(), "the latest schedule must leave its evaluation pending")
	h.fireOptimizeNoticeSync()
	h.Wait()
	assert.Equal(t, int32(1), evaluations.Load(), "a burst of schedules runs exactly one evaluation")
	created, _ := notices.counts()
	assert.Equal(t, 1, created)

	// Stop cancels the pending timer, and later schedules arm nothing.
	h.ScheduleOptimizeNoticeSync()
	pending := pendingOptimizeTimer(h)
	require.NotNil(t, pending)
	h.StopOptimizeNoticeSync()
	assert.False(t, pending.Stop(), "StopOptimizeNoticeSync must cancel the pending evaluation")
	h.ScheduleOptimizeNoticeSync()
	assert.Nil(t, pendingOptimizeTimer(h), "no schedule is armed after StopOptimizeNoticeSync")
}

// TestSyncOptimizeNotice_ProcessWideService runs the notice through the real
// notification service (no test seam): the notice lands in the store with its
// translation keys. Not parallel: it uses the process-wide notification service.
func TestSyncOptimizeNotice_ProcessWideService(t *testing.T) {
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)

	profile := aarch64LowRAMONNXProfile()
	h, _ := newOptimizeTestHandler(t, &profile)
	h.notices = nil // use the process-wide service

	h.syncOptimizeNotice()

	stored, err := svc.List(&notification.FilterOptions{Types: []notification.Type{notification.TypeInfo}})
	require.NoError(t, err)
	var found *notification.Notification
	for _, n := range stored {
		if n.TitleKey == notification.MsgModelOptimizeTitle {
			found = n
		}
	}
	require.NotNil(t, found, "the optimize notice must reach the notification store")
	assert.Equal(t, found.ID, h.optimize.latch.ID(), "the stored notice is the latched one")
}

// TestSyncOptimizeNotice_ServiceNotInitialized pins that a sync before the
// notification service exists is a safe no-op that runs no evaluation and
// latches nothing. Not parallel: it resets the process-wide notification service.
func TestSyncOptimizeNotice_ServiceNotInitialized(t *testing.T) {
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)

	profile := aarch64LowRAMONNXProfile()
	h, _, evaluations := newCountingOptimizeTestHandler(t, &profile)
	h.notices = nil

	assert.NotPanics(t, h.syncOptimizeNotice)
	assert.Empty(t, h.optimize.latch.ID())
	assert.Zero(t, evaluations.Load(), "no evaluation runs without a notification service")

	var nilHandler *Handler
	assert.NotPanics(t, nilHandler.StopOptimizeNoticeSync)
	assert.NotPanics(t, nilHandler.ScheduleOptimizeNoticeSync)
	assert.NotPanics(t, nilHandler.StartOptimizeNoticeSync)
}

// optimizeTimerArmed reports whether an optimize notice evaluation is pending.
func optimizeTimerArmed(h *Handler) bool {
	h.optimize.timerMu.Lock()
	defer h.optimize.timerMu.Unlock()
	return h.optimize.timer != nil
}

// TestStartOptimizeNoticeSync_GatesScheduling pins that schedules are ignored
// until the handler is started (an unrouted controller never arms a timer from
// its install or uninstall paths) and that Start itself arms the startup
// evaluation.
func TestStartOptimizeNoticeSync_GatesScheduling(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, _ := newOptimizeTestHandler(t, &profile)
	t.Cleanup(h.StopOptimizeNoticeSync)

	h.ScheduleOptimizeNoticeSync()
	assert.False(t, optimizeTimerArmed(h), "a schedule before Start must not arm a timer")

	h.StartOptimizeNoticeSync()
	assert.True(t, optimizeTimerArmed(h), "Start arms the startup evaluation")

	h.StopOptimizeNoticeSync()
	h.StartOptimizeNoticeSync()
	assert.False(t, optimizeTimerArmed(h), "Start after Stop must not re-arm")
}

// TestFireOptimizeNoticeSync_AfterStopDoesNothing pins that a timer callback
// that fires after StopOptimizeNoticeSync (it was already dequeued when Stop
// ran) starts no evaluation.
func TestFireOptimizeNoticeSync_AfterStopDoesNothing(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices, evaluations := newCountingOptimizeTestHandler(t, &profile)

	h.StopOptimizeNoticeSync()
	h.fireOptimizeNoticeSync()
	h.Wait()

	assert.Zero(t, evaluations.Load())
	created, _ := notices.counts()
	assert.Zero(t, created)
}

// TestFireOptimizeNoticeSync_JoinedByCoreWait pins that an evaluation already
// running when the controller shuts down is joined by Core.Wait, so it cannot
// outlive Shutdown.
func TestFireOptimizeNoticeSync_JoinedByCoreWait(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, _, _ := newCountingOptimizeTestHandler(t, &profile)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseEval := func() { releaseOnce.Do(func() { close(release) }) }
	// Registered after apitest.NewCore, so it runs before the Core's Wait: a
	// failed assertion below cannot leave the evaluation blocked and hang cleanup.
	t.Cleanup(releaseEval)
	var once sync.Once
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile {
		once.Do(func() { close(entered) })
		<-release
		return profile
	}

	h.fireOptimizeNoticeSync()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		require.Fail(t, "the fired callback never started an evaluation")
	}
	h.StopOptimizeNoticeSync()

	waited := make(chan struct{})
	go func() {
		h.Wait()
		close(waited)
	}()
	assert.Never(t, func() bool {
		select {
		case <-waited:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond, "Core.Wait returned while the evaluation was still running")

	releaseEval()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		require.Fail(t, "Core.Wait did not return after the evaluation finished")
	}
}

// installFakeModel puts the default variant's model file of catalogID on disk so
// ScanInstalled reports it installed, without downloading anything.
func installFakeModel(t *testing.T, modelsDir, catalogID string) {
	t.Helper()
	entry, ok := classifier.GetCatalogEntry(catalogID)
	require.True(t, ok)
	dir := filepath.Join(modelsDir, catalogID)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for _, f := range entry.Files {
		if f.Role == "model" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, f.LocalName), []byte("fake"), 0o600))
		}
	}
}

// TestModelHandlers_ScheduleOptimizeNoticeSync pins that install, reinstall and
// a successful uninstall re-evaluate the optimize notice. None of them reliably
// fires a topology event (an unloaded model's uninstall, an install whose
// hot-load fails), so the handlers schedule it themselves.
func TestModelHandlers_ScheduleOptimizeNoticeSync(t *testing.T) {
	// Each operation gets a fresh handler and models directory: a failed
	// download leaves the model in a retained failed state that would block the
	// next operation on the same manager.
	setup := func(t *testing.T, installed bool) (*Handler, *apicore.Core) {
		t.Helper()
		core := apitest.NewCore(t)
		modelsDir := t.TempDir()
		if installed {
			installFakeModel(t, modelsDir, "perch-v2")
		}
		mm := classifier.NewModelManager(modelsDir, nil, nil)
		mm.ScanInstalled()
		_, isInstalled := mm.InstalledVariantID("perch-v2")
		require.Equal(t, installed, isInstalled, "fixture: perch-v2 install state")
		core.ModelManager = mm
		h := New(core, nil)
		profile := amd64ONNXProfile() // perch-v2 must pass the install compatibility gate
		h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return profile }
		h.notices = &fakeNotices{}
		t.Cleanup(h.StopOptimizeNoticeSync)
		// Arm Start's timer, then clear it so the handler's own schedule is visible.
		h.StartOptimizeNoticeSync()
		h.optimize.timerMu.Lock()
		h.optimize.timer.Stop()
		h.optimize.timer = nil
		h.optimize.timerMu.Unlock()
		// Install and reinstall run in Core-tracked goroutines; with the Core
		// context already cancelled the download fails at once, without network
		// access, and the schedule after it still runs. Uninstall is synchronous.
		core.Cancel()
		return h, core
	}
	call := func(t *testing.T, handler echo.HandlerFunc, method string) int {
		t.Helper()
		e := echo.New()
		req := httptest.NewRequest(method, "/api/v2/models/perch-v2", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("id")
		ctx.SetParamValues("perch-v2")
		require.NoError(t, handler(ctx))
		return rec.Code
	}

	t.Run("uninstall", func(t *testing.T) {
		h, _ := setup(t, true)
		require.Equal(t, http.StatusOK, call(t, h.UninstallModel, http.MethodDelete))
		assert.True(t, optimizeTimerArmed(h), "a successful uninstall schedules a re-evaluation")
	})
	t.Run("install", func(t *testing.T) {
		h, core := setup(t, false)
		require.Equal(t, http.StatusAccepted, call(t, h.InstallModel, http.MethodPost))
		core.Wait()
		assert.True(t, optimizeTimerArmed(h), "an install schedules a re-evaluation, even when it fails")
	})
	t.Run("reinstall", func(t *testing.T) {
		h, core := setup(t, true)
		require.Equal(t, http.StatusAccepted, call(t, h.ReinstallModel, http.MethodPost))
		core.Wait()
		assert.True(t, optimizeTimerArmed(h), "a reinstall schedules a re-evaluation, even when it fails")
	})
}
