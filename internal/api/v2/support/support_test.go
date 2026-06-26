package support

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestSupportRouteRegistration verifies the support handler registers exactly the
// support endpoints, with the same methods and paths the monolithic facade used
// before the domain was extracted.
//
// RegisterRoutes also starts two background goroutines (guarded by a non-nil
// lifecycle context): the app-event-pruning goroutine, which prunes once at
// startup via DS.PruneAppEvents before blocking on the context, and the
// support-dump cleanup goroutine, which runs cleanupOldSupportDumps() once at
// startup (a glob of os.TempDir() that removes only birdnet-go-support-*.zip
// files older than an hour, identical to production) before blocking on the
// context. apitest.NewCore wires a non-nil lifecycle context and cancels +
// waits for it on cleanup, so both goroutines start and stop cleanly here; the
// PruneAppEvents expectation below is marked Maybe because it may or may not run
// before that cancellation.
func TestSupportRouteRegistration(t *testing.T) {
	e := echo.New()

	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().
		PruneAppEvents(mock.Anything, mock.Anything).
		Return(int64(0), nil).
		Maybe()

	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mockDS))
	h := New(core)

	h.RegisterRoutes(core.Group)

	expectedRoutes := []string{
		"POST /api/v2/support/generate",
		"GET /api/v2/support/download/:id",
		"GET /api/v2/support/status",
	}
	apitest.AssertRoutesRegistered(t, e, expectedRoutes)
}
