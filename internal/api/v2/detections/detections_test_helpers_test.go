// detections_test_helpers_test.go: shared scaffolding for the detections domain
// tests.
//
// Core-level scaffolding (settings builder, mock metrics/image cache, route
// assertions, the *apicore.Core builder) lives in the importable
// internal/api/v2/apitest package. The helpers here build the detections Handler
// with in-memory test doubles for the facade-injected dependencies (settings-save
// machinery, auth check, and name-map accessors), mirroring the integrations domain
// tests.
package detections

import (
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// buildTestHandler builds a detections Handler around an apitest core with
// in-memory test doubles for the facade-injected dependencies. The settings-save
// doubles mirror the production behavior: getSettingsOrFallback reads the core's
// atomic snapshot, publishAndSaveSettings stores the updated snapshot back (no disk
// write, matching the original DisableSaveSettings=true), and handleSettingsChanges
// is a no-op. isClientAuthenticated defaults to unauthenticated (the facade test
// controller never injected an auth service). The name-map accessors return the
// supplied maps so a test can seed name resolution.
func buildTestHandler(t *testing.T, core *apicore.Core, commonToSci, sciToCommon map[string]string) *Handler {
	t.Helper()
	var mu sync.RWMutex
	return New(core, &mu,
		func() *conf.Settings { return core.Settings.Load() },
		func(_, updated *conf.Settings) error { core.Settings.Store(updated); return nil },
		func(_, _ *conf.Settings) error { return nil },
		func(echo.Context) bool { return false },
		func() map[string]string { return sciToCommon },
		func() map[string]string { return commonToSci },
	)
}

// setupTestEnvironment builds an Echo, a mock datastore, and a detections Handler
// wired through an apitest core, with empty name maps. It mirrors the package-api
// helper of the same name (which returned a *Controller); the handler's settings
// route group is wired (core.Group) so route-registration tests can register routes.
func setupTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()
	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mockDS))
	h := buildTestHandler(t, core, map[string]string{}, map[string]string{})
	return e, mockDS, h
}

// setupTestEnvironmentWithBatName is setupTestEnvironment with the name maps
// pre-seeded so the Finnish bat common name "mopsilepakko" resolves to the
// scientific name "Barbastella barbastellus" (a secondary-model species whose label
// carries no embedded common name). It replaces the package-api tests' facade
// SetNameResolver + UpdateCommonNameMap setup, exercising the same observable
// name-resolution behavior without the facade name-map plumbing (which is tested in
// its own package-api tests).
func setupTestEnvironmentWithBatName(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()
	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mockDS))
	commonToSci := map[string]string{apicore.NormalizeForLookup("mopsilepakko"): "Barbastella barbastellus"}
	sciToCommon := map[string]string{"Barbastella barbastellus": "mopsilepakko"}
	h := buildTestHandler(t, core, commonToSci, sciToCommon)
	return e, mockDS, h
}

// setupValidReviewMock configures mock expectations for a valid review operation.
// Used for detection review tests where the note is not locked and saves succeed.
func setupValidReviewMock(m *mock.Mock, id string, noteID uint, withComment bool) {
	m.On("Get", id).Return(datastore.Note{ID: noteID, Locked: false}, nil)
	m.On("IsNoteLocked", id).Return(false, nil)
	if withComment {
		m.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil)
	}
	m.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)
}
