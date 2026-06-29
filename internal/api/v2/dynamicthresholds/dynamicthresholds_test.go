package dynamicthresholds

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestGetMergedThresholdData_NoDuplicates(t *testing.T) {
	t.Parallel()

	mockDS := mocks.NewMockInterface(t)
	now := time.Now()
	expires := now.Add(24 * time.Hour)

	// Database returns Title Case species name with ModelName (as resolveCommonName does)
	mockDS.EXPECT().GetAllDynamicThresholds().Return([]datastore.DynamicThreshold{
		{
			SpeciesName:    "Tawny Owl",
			ModelName:      "BirdNET",
			ScientificName: "Strix aluco",
			Level:          1,
			CurrentValue:   0.45,
			BaseThreshold:  0.6,
			HighConfCount:  1,
			ExpiresAt:      expires,
		},
	}, nil)

	// Processor memory stores with composite key "modelID:speciesLowercase"
	proc := &processor.Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{Threshold: 0.6},
		},
		DynamicThresholds: map[string]*processor.DynamicThreshold{
			"BirdNET:tawny owl": {
				Level:          2,
				CurrentValue:   0.3,
				Timer:          expires,
				HighConfCount:  2,
				ValidHours:     24,
				ScientificName: "Strix aluco",
			},
		},
	}

	// The merge tests construct the Core directly (rather than via apitest.NewCore)
	// because they need to inject a *processor.Processor, which apitest.NewCore does
	// not expose; the NotFound tests below use apitest.NewCore (Processor stays nil).
	handler := &Handler{Core: &apicore.Core{DS: mockDS, Processor: proc}}
	handler.Settings.Store(proc.Settings)

	result := handler.getMergedThresholdData()

	// Bug: before fix this returns 2 entries (one Title Case, one lowercase)
	// After fix: should return exactly 1 entry with memory overlay applied
	require.Len(t, result, 1, "should merge same species regardless of case")

	// Find the single entry
	var entry *DynamicThresholdResponse
	for _, v := range result {
		entry = v
	}
	require.NotNil(t, entry)

	// Memory data should override database data (memory is more current)
	assert.Equal(t, 2, entry.Level, "level should come from memory overlay")
	assert.InDelta(t, 0.3, entry.CurrentValue, 0.001, "current value should come from memory overlay")
	// Display name should be Title Case (from database, the proper display name)
	assert.Equal(t, "Tawny Owl", entry.SpeciesName, "display name should be Title Case from database")
}

func TestGetMergedThresholdData_MemoryOnlySpecies(t *testing.T) {
	t.Parallel()

	mockDS := mocks.NewMockInterface(t)
	expires := time.Now().Add(24 * time.Hour)

	// Database returns no thresholds
	mockDS.EXPECT().GetAllDynamicThresholds().Return([]datastore.DynamicThreshold{}, nil)

	// Processor memory has a species not in the database
	proc := &processor.Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{Threshold: 0.6},
		},
		DynamicThresholds: map[string]*processor.DynamicThreshold{
			"BirdNET:eurasian blue tit": {
				Level:          1,
				CurrentValue:   0.45,
				Timer:          expires,
				HighConfCount:  1,
				ValidHours:     24,
				ScientificName: "Cyanistes caeruleus",
			},
		},
	}

	handler := &Handler{Core: &apicore.Core{DS: mockDS, Processor: proc}}
	handler.Settings.Store(proc.Settings)

	result := handler.getMergedThresholdData()

	require.Len(t, result, 1, "memory-only species should appear")
	var entry *DynamicThresholdResponse
	for _, v := range result {
		entry = v
	}
	require.NotNil(t, entry)
	assert.Equal(t, "eurasian blue tit", entry.SpeciesName)
	assert.Equal(t, "Cyanistes caeruleus", entry.ScientificName)
}

func TestGetMergedThresholdData_DatabaseOnlySpecies(t *testing.T) {
	t.Parallel()

	mockDS := mocks.NewMockInterface(t)
	now := time.Now()
	expires := now.Add(24 * time.Hour)

	// Database has a species
	mockDS.EXPECT().GetAllDynamicThresholds().Return([]datastore.DynamicThreshold{
		{
			SpeciesName:    "Common Blackbird",
			ModelName:      "BirdNET",
			ScientificName: "Turdus merula",
			Level:          1,
			CurrentValue:   0.45,
			BaseThreshold:  0.6,
			HighConfCount:  1,
			ExpiresAt:      expires,
		},
	}, nil)

	// Processor has no thresholds (empty map)
	proc := &processor.Processor{
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{Threshold: 0.6},
		},
		DynamicThresholds: map[string]*processor.DynamicThreshold{},
	}

	handler := &Handler{Core: &apicore.Core{DS: mockDS, Processor: proc}}
	handler.Settings.Store(proc.Settings)

	result := handler.getMergedThresholdData()

	require.Len(t, result, 1, "database-only species should appear")
	var entry *DynamicThresholdResponse
	for _, v := range result {
		entry = v
	}
	require.NotNil(t, entry)
	assert.Equal(t, "Common Blackbird", entry.SpeciesName)
	assert.Equal(t, "Turdus merula", entry.ScientificName)
	assert.Equal(t, 1, entry.Level)
}

// TestGetDynamicThreshold_NotFoundStatus pins both sides of the handler's not-found
// classification boundary that #1068 turned on. HandleErrorWithNotFound maps
// a CategoryNotFound EnhancedError to 404 but an unclassified error (a bare sentinel)
// to 500. The v2only datastore used to return the bare ErrDynamicThresholdNotFound
// sentinel, so a missing threshold fell through to 500 while the legacy backend
// returned 404; the datastore fix wraps the sentinel as CategoryNotFound. The first
// subtest pins the fixed behavior; the second pins the bare-sentinel regression so a
// future unwrapped return is caught here too.
func TestGetDynamicThreshold_NotFoundStatus(t *testing.T) {
	const species = "Nonexistent species"

	t.Run("CategoryNotFoundMapsTo404", func(t *testing.T) {
		e, mockDS, handler := newThresholdsTestHandler(t)
		// What the fixed v2only datastore (and the legacy backend) returns.
		notFound := errors.New(errors.NewStd("dynamic threshold not found")).
			Component("datastore").
			Category(errors.CategoryNotFound).
			Build()
		mockDS.EXPECT().GetDynamicThreshold(species, "").Return(nil, notFound)

		rec := serveGetDynamicThreshold(t, e, handler, species)
		assert.Equal(t, http.StatusNotFound, rec.Code,
			"a CategoryNotFound threshold miss must map to 404, not 500")
	})

	t.Run("BareSentinelMapsTo500", func(t *testing.T) {
		e, mockDS, handler := newThresholdsTestHandler(t)
		// The pre-fix v2only behavior: a bare, unclassified sentinel. The handler
		// cannot tell it is a benign not-found, so it falls through to 500. This is
		// the #1068 bug, pinned so a future unwrapped return is caught at the handler.
		mockDS.EXPECT().GetDynamicThreshold(species, "").
			Return(nil, errors.NewStd("dynamic threshold not found"))

		rec := serveGetDynamicThreshold(t, e, handler, species)
		assert.Equal(t, http.StatusInternalServerError, rec.Code,
			"an unclassified (bare sentinel) threshold miss falls through to 500")
	})
}

// newThresholdsTestHandler builds a dynamic-thresholds Handler around an
// apicore.Core (via apitest) with a mock datastore the caller can set
// expectations on. apitest.NewCore leaves Processor nil, so GetDynamicThreshold's
// processor-overlay branch is skipped and only DS.GetDynamicThreshold is exercised.
func newThresholdsTestHandler(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()
	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mockDS))
	return e, mockDS, New(core)
}

// serveGetDynamicThreshold issues GET /api/v2/dynamic-thresholds/:species against the
// handler with the given species path param and returns the response recorder. The
// handler writes the response via HandleError and returns nil (ctx.JSON success), so
// the observable contract is the recorded status code.
func serveGetDynamicThreshold(t *testing.T, e *echo.Echo, handler *Handler, species string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/dynamic-thresholds/test", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("species")
	ctx.SetParamValues(species)
	require.NoError(t, handler.GetDynamicThreshold(ctx))
	return rec
}
