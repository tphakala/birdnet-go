package models

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
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

// TestEnsureHostProbed_BeforeLiveRanking pins that the rankings that decide
// something lasting (the optimize notice and the install gate) run the
// out-of-process OpenVINO probe first when they use the live host profile, and
// never under the test profile seam.
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

	// Live host profile (no seam): both callers probe first.
	h.currentOptimizeOffers()
	assert.Equal(t, int32(1), probes.Load(), "the optimize evaluation probes before ranking")
	h.requestedVariantCompatibility(&entry, "", inference.ORTStatus{})
	assert.Equal(t, int32(2), probes.Load(), "the install gate probes before ranking")

	// Synthetic profile: nothing to probe.
	profile := tfliteOnlyProfile()
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return profile }
	h.currentOptimizeOffers()
	h.requestedVariantCompatibility(&entry, "", inference.ORTStatus{})
	assert.Equal(t, int32(2), probes.Load(), "the test profile seam never probes")
}

func TestOptimizeOffersSignature_OrderIndependent(t *testing.T) {
	t.Parallel()
	a := optimizeOffer{CatalogID: "a", ToVariantID: "fast"}
	b := optimizeOffer{CatalogID: "b", ToVariantID: "fast"}
	assert.Equal(t, optimizeOffersSignature([]optimizeOffer{a, b}), optimizeOffersSignature([]optimizeOffer{b, a}))
	assert.NotEqual(t, optimizeOffersSignature([]optimizeOffer{a}), optimizeOffersSignature([]optimizeOffer{a, b}))
	assert.Empty(t, optimizeOffersSignature(nil))
}

// TestSyncOptimizeNotice_RaisesForBuiltinOnRecommendedHost is the #4423 scenario:
// a Raspberry Pi class host still on the built-in BirdNET v2.4 baseline gets one
// bell notice naming the model, with translation keys and bell-only delivery.
func TestSyncOptimizeNotice_RaisesForBuiltinOnRecommendedHost(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices := newOptimizeTestHandler(t, &profile)

	h.SyncOptimizeNotice()

	require.Len(t, notices.created, 1)
	n := notices.created[0]
	assert.Equal(t, notification.TypeInfo, n.Type)
	assert.Equal(t, notification.DeliveryTargetBell, n.DeliveryTarget)
	assert.Equal(t, optimizeNoticeComponent, n.Component)
	assert.Equal(t, notification.MsgModelOptimizeTitle, n.TitleKey)
	assert.Equal(t, notification.MsgModelOptimizeMessage, n.MessageKey)
	assert.Equal(t, 1, n.TitleParams["count"])
	assert.Contains(t, n.MessageParams["models"], "BirdNET")
	assert.Equal(t, "1 model has a better build for this system", n.Title)
	assert.Equal(t, 1, n.Metadata[optimizeOfferCountMetadataKey])
}

// TestSyncOptimizeNotice_Lifecycle covers the latch: an unchanged offer set is a
// no-op, an emptied set clears the notice, and a returning set raises a new one.
func TestSyncOptimizeNotice_Lifecycle(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices := newOptimizeTestHandler(t, &profile)

	h.SyncOptimizeNotice()
	h.SyncOptimizeNotice()
	created, deleted := notices.counts()
	assert.Equal(t, 1, created, "an unchanged offer set must not raise a second notice")
	assert.Zero(t, deleted)

	// The host now recommends the installed baseline: the offer is gone.
	profile = tfliteOnlyProfile()
	h.SyncOptimizeNotice()
	created, deleted = notices.counts()
	assert.Equal(t, 1, created)
	require.Equal(t, 1, deleted, "an emptied offer set must clear the notice")
	assert.Equal(t, notices.created[0].ID, notices.deleted[0])

	// Nothing outstanding and still no offer: no-op.
	h.SyncOptimizeNotice()
	created, deleted = notices.counts()
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, deleted)

	// The offer returns: a fresh notice is raised.
	profile = aarch64LowRAMONNXProfile()
	h.SyncOptimizeNotice()
	created, _ = notices.counts()
	assert.Equal(t, 2, created)
}

func TestSyncOptimizeNotice_NoOfferNoNotice(t *testing.T) {
	profile := tfliteOnlyProfile()
	h, notices := newOptimizeTestHandler(t, &profile)

	h.SyncOptimizeNotice()

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
	h.SyncOptimizeNotice()
	created, _ := notices.counts()
	require.Zero(t, created)

	notices.createErr = nil
	h.SyncOptimizeNotice()
	created, _ = notices.counts()
	assert.Equal(t, 1, created, "a failed create must be retried on the next sync")
}

func TestSyncOptimizeNotice_NilModelManagerIsNoop(t *testing.T) {
	t.Parallel()
	h := New(apitest.NewCore(t), nil)
	notices := &fakeNotices{}
	h.notices = notices

	h.SyncOptimizeNotice()
	h.ScheduleOptimizeNoticeSync()

	created, deleted := notices.counts()
	assert.Zero(t, created)
	assert.Zero(t, deleted)
	h.optimize.timerMu.Lock()
	defer h.optimize.timerMu.Unlock()
	assert.Nil(t, h.optimize.timer, "no evaluation is scheduled without a ModelManager")
}

// TestScheduleOptimizeNoticeSync_DebouncesAndStops verifies a burst of schedules
// runs one evaluation after the debounce, and that nothing fires after Stop.
func TestScheduleOptimizeNoticeSync_DebouncesAndStops(t *testing.T) {
	profile := aarch64LowRAMONNXProfile()
	h, notices, evaluations := newCountingOptimizeTestHandler(t, &profile)

	synctest.Test(t, func(t *testing.T) {
		for range 5 {
			h.ScheduleOptimizeNoticeSync()
		}
		time.Sleep(optimizeNoticeDebounce - time.Millisecond)
		synctest.Wait()
		assert.Zero(t, evaluations.Load(), "no evaluation before the debounce elapses")

		time.Sleep(time.Millisecond)
		synctest.Wait()
		assert.Equal(t, int32(1), evaluations.Load(), "a burst of schedules runs exactly one evaluation")
		created, _ := notices.counts()
		assert.Equal(t, 1, created)

		// A schedule stopped before it fires never runs, and later ones are ignored.
		profile = tfliteOnlyProfile()
		h.ScheduleOptimizeNoticeSync()
		h.StopOptimizeNotice()
		h.ScheduleOptimizeNoticeSync()
		time.Sleep(2 * optimizeNoticeDebounce)
		synctest.Wait()
		assert.Equal(t, int32(1), evaluations.Load(), "no evaluation runs after StopOptimizeNotice")
		_, deleted := notices.counts()
		assert.Zero(t, deleted)
	})
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

	h.SyncOptimizeNotice()

	stored, err := svc.List(&notification.FilterOptions{Types: []notification.Type{notification.TypeInfo}})
	require.NoError(t, err)
	var found *notification.Notification
	for _, n := range stored {
		if n.TitleKey == notification.MsgModelOptimizeTitle {
			found = n
		}
	}
	require.NotNil(t, found, "the optimize notice must reach the notification store")
	assert.Equal(t, found.ID, h.optimize.id, "the stored notice is the latched one")
}

// TestSyncOptimizeNotice_ServiceNotInitialized pins that a sync before the
// notification service exists is a safe no-op that latches nothing, so the next
// trigger retries. Not parallel: it resets the process-wide notification service.
func TestSyncOptimizeNotice_ServiceNotInitialized(t *testing.T) {
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)

	profile := aarch64LowRAMONNXProfile()
	h, _, evaluations := newCountingOptimizeTestHandler(t, &profile)
	h.notices = nil

	assert.NotPanics(t, h.SyncOptimizeNotice)
	assert.Empty(t, h.optimize.id)
	assert.Zero(t, evaluations.Load(), "no evaluation runs without a notification service")

	var nilHandler *Handler
	assert.NotPanics(t, nilHandler.StopOptimizeNotice)
	assert.NotPanics(t, nilHandler.ScheduleOptimizeNoticeSync)
}
