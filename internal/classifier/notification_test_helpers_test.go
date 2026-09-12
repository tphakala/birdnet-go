package classifier

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/notification"
)

// setupTestNotification initializes the process-global notification service for a
// test and registers the cleanups that reset it and stop the running service.
// It returns the service so a caller that needs the handle can use it directly.
//
// Cleanups run LIFO, so svc.Stop() runs before the final ResetForTest(), matching
// the ordering of the bootstrap block this consolidates (ResetForTest, Initialize,
// GetService, require.NotNil, Cleanup(Stop)) that several classifier tests repeated.
func setupTestNotification(t *testing.T) *notification.Service {
	t.Helper()
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)
	return svc
}
