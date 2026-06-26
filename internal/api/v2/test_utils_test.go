// Shared test utilities for API v2 facade (package api) tests.
//
// Core-level scaffolding (settings builders, mock image providers, metrics, HTTP
// helpers, route assertions, and the *apicore.Core builder) lives in the
// importable internal/api/v2/apitest package. The helpers here are the thin
// facade glue that builds a *Controller, which apitest cannot do (it must not
// import this package).

package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Test environment constants
const (
	testHelsinkiLatitude  = 60.1699 // Helsinki, Finland latitude for SunCalc tests
	testHelsinkiLongitude = 24.9384 // Helsinki, Finland longitude for SunCalc tests
	testControlChannelBuf = 10      // Control channel buffer size for concurrent test scenarios
)

// newMinimalController creates a Controller for simple validation tests.
// Use this for tests that only need to call handler methods without database or full infrastructure.
// Includes default settings to prevent nil pointer panics if a handler accesses c.Settings.
func newMinimalController() *Controller {
	c := &Controller{Core: &apicore.Core{}}
	c.Settings.Store(apitest.NewValidTestSettings())
	return c
}

// setupAnalyticsTestEnvironment creates a test environment with Echo, mocks.MockInterface, and Controller
// for analytics tests
func setupAnalyticsTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Controller) {
	t.Helper()
	// Create a new Echo instance
	e := echo.New()

	// Create a test datastore
	mockDS := mocks.NewMockInterface(t)

	// Create a controller with the test datastore and default settings
	// to prevent nil pointer panics if a handler accesses c.Settings.
	controller := &Controller{Core: &apicore.Core{Group: e.Group(apiV2Prefix), DS: mockDS}}
	controller.Settings.Store(apitest.NewValidTestSettings())

	// Don't initialize routes as it causes nil pointer dereference in tests
	// controller.initRoutes()

	return e, mockDS, controller
}

// setupTestEnvironment creates a complete facade test environment.
//
// It builds a real *Controller via the production NewWithOptions constructor
// (initializeRoutes=false to avoid starting background route goroutines), wiring:
//  1. Echo instance - a new web framework instance for handling HTTP requests
//  2. mocks.MockInterface - a generated mock datastore for test data
//  3. Settings - shared valid defaults with a per-test temp export path
//  4. A mock BirdImageCache whose Fetch returns an empty placeholder image
//  5. SunCalc - a sun calculator seeded with Helsinki coordinates
//  6. Control channel - buffered to avoid blocking in tests
//
// The Core-level building blocks (settings, mock cache, metrics, settings publish)
// come from the apitest package so they are not duplicated here. It returns the
// Echo instance, mock datastore, and Controller. Cleanup (Shutdown + channel
// close) is registered via t.Cleanup.
func setupTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Controller) {
	t.Helper()

	// Create Echo instance
	e := echo.New()

	// Create mock datastore
	mockDS := mocks.NewMockInterface(t)

	// Settings from shared valid defaults, with a per-test temp export path for
	// isolation (the media SecureFS is rooted here).
	settings := apitest.NewValidTestSettings()
	settings.Realtime.Audio.Export.Path = t.TempDir()

	// Mock image cache (default Fetch returns an empty placeholder image).
	birdImageCache := apitest.NewMockBirdImageCache(t)

	// Sun calculator with test coordinates (Helsinki, Finland)
	sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)

	// Buffered control channel to prevent blocking in tests. Size 10 is sufficient
	// for concurrent test scenarios (e.g., TestConcurrentControlRequests uses 5).
	controlChan := make(chan string, testControlChannelBuf)

	// Mock metrics for testing
	mockMetrics := apitest.NewTestMetrics(t)

	// Publish settings to the process-global snapshot so functions like
	// conf.SaveSettings() (called by toggleSpeciesInIgnoredList) and handlers
	// reading via currentSettings() operate on this controller's settings.
	// Restored on cleanup so it does not leak into sibling tests.
	apitest.PublishTestSettings(t, settings)

	// Create API controller without initializing routes to avoid starting background goroutines
	controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, mockMetrics, false)
	require.NoError(t, err, "Failed to create test API controller")
	// Handler tests assert on in-memory settings and the HTTP response, not on
	// disk persistence. Disable disk saves so settings-mutating handlers (e.g.
	// IgnoreSpecies -> toggleSpeciesInIgnoredList -> conf.SaveSettings) do not
	// write to the real default config path. On Windows that write fails (the
	// default config directory is not provisioned in the test environment),
	// surfacing as a 500; on Linux it silently pollutes the machine's config.
	// All dedicated settings tests already set this flag.
	controller.DisableSaveSettings = true

	// Register cleanup to stop background goroutines
	t.Cleanup(func() {
		// Shutdown the controller properly. This also closes the media SecureFS
		// (an open os.Root on the t.TempDir() export path); on Windows that handle
		// must be released or t.TempDir()'s RemoveAll cannot delete the directory.
		controller.Shutdown()
		// Close control channel to signal goroutines to exit
		close(controlChan)
	})

	return e, mockDS, controller
}

// SpeciesDailySummaryExpected contains expected values for species daily summary assertions.
type SpeciesDailySummaryExpected struct {
	CommonName          string
	SpeciesCode         string
	Count               int
	HourlyCounts        []int
	FirstHeard          string
	LatestHeard         string
	HighConfidence      bool
	ThumbnailURLContain string // Substring that ThumbnailURL should contain
}

// assertSpeciesDailySummary verifies that a SpeciesDailySummary matches expected values.
// This reduces duplication in tests that verify species summary fields.
func assertSpeciesDailySummary(t *testing.T, species *SpeciesDailySummary, expected *SpeciesDailySummaryExpected) {
	t.Helper()

	assert.Equal(t, expected.CommonName, species.CommonName, "%s common name mismatch", expected.CommonName)
	assert.Equal(t, expected.SpeciesCode, species.SpeciesCode, "%s species code mismatch", expected.CommonName)
	assert.Equal(t, expected.Count, species.Count, "%s count mismatch", expected.CommonName)
	assert.Equal(t, expected.HourlyCounts, species.HourlyCounts, "%s hourly counts mismatch", expected.CommonName)
	assert.Equal(t, expected.FirstHeard, species.FirstHeard, "%s first heard time mismatch", expected.CommonName)
	assert.Equal(t, expected.LatestHeard, species.LatestHeard, "%s latest heard time mismatch", expected.CommonName)
	assert.Equal(t, expected.HighConfidence, species.HighConfidence, "%s high confidence mismatch", expected.CommonName)
	assert.Contains(t, species.ThumbnailURL, expected.ThumbnailURLContain, "%s thumbnail URL mismatch", expected.CommonName)
}

// setupValidReviewMock configures mock expectations for a valid review operation.
// This is used for detection review tests where the note is not locked and saves succeed.
func setupValidReviewMock(m *mock.Mock, id string, noteID uint, withComment bool) {
	m.On("Get", id).Return(datastore.Note{ID: noteID, Locked: false}, nil)
	m.On("IsNoteLocked", id).Return(false, nil)
	if withComment {
		m.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil)
	}
	m.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)
}
