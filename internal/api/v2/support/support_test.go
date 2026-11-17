package support

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestRegisterRoutesGatesEveryRoute tests that every support route is registered
// and sits behind the auth middleware.
//
// RegisterRoutes also starts the support dump cleanup and app event pruning
// goroutines. apitest.NewCore cancels and waits for them on cleanup, and the
// PruneAppEvents expectation is Maybe because the cancellation can come first.
func TestRegisterRoutesGatesEveryRoute(t *testing.T) {
	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().
		PruneAppEvents(mock.Anything, mock.Anything).
		Return(int64(0), nil).
		Maybe()

	core := apitest.NewCore(t, apitest.WithDatastore(mockDS))
	h := New(core)

	apitest.AssertRoutesRequireAuth(t, core, h.RegisterRoutes, []string{
		"POST /api/v2/support/generate",
		"GET /api/v2/support/download/:id",
		"GET /api/v2/support/status",
	})
}
