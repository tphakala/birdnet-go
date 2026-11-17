package notifications

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/labstack/echo/v4"
)

// requestWithCancelCtx builds an *http.Request whose context can be cancelled to
// simulate an SSE client disconnect, with the given RemoteAddr for RealIP.
func requestWithCancelCtx(t *testing.T, remoteAddr string) (*http.Request, context.CancelFunc) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/notifications/stream", http.NoBody)
	req.RemoteAddr = remoteAddr
	baseCtx, cancel := context.WithCancel(t.Context())
	return req.WithContext(baseCtx), cancel
}

// recycledRequest builds the request a pooled echo.Context is rebound to after
// its handler returns, with its own header map and remote address.
func recycledRequest() *http.Request {
	next := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	next.Header.Set(echo.HeaderXForwardedFor, "2.2.2.2")
	next.RemoteAddr = "2.2.2.2:2222"
	return next
}

// TestSetupNotificationDisconnectHandler_NoContextRaceAfterHandlerReturn is a
// regression test for issue #4292 ("fatal error: concurrent map read and map
// write"). The disconnect watcher goroutine outlives the SSE handler, and Echo
// pools and recycles the echo.Context once the handler returns, rebinding its
// *http.Request to the next in-flight request. Reading ctx (ctx.Request(),
// ctx.RealIP()) from the watcher therefore races with the request the pooled
// context is next bound to.
//
// The test simulates the recycle by rebinding the context's request, once from
// another goroutine around the client disconnect and once from the test
// goroutine after the watcher has signalled the disconnect. Nothing orders
// either write against a read of ctx made by the watcher, so the test depends on
// the race detector (run with -race): it fails if the watcher reads ctx at any
// point after setup, and passes because the watcher captures the request context
// and client IP up front and never reads ctx afterwards.
func TestSetupNotificationDisconnectHandler_NoContextRaceAfterHandlerReturn(t *testing.T) {
	t.Parallel()

	// The handler is built outside the bubble.
	c := mockHandler()

	synctest.Test(t, func(t *testing.T) {
		e := echo.New()
		req, cancel := requestWithCancelCtx(t, "1.1.1.1:1111")
		defer cancel()
		ctx := e.NewContext(req, httptest.NewRecorder())

		client := &NotificationClient{
			ID:   "race-client",
			Done: make(chan struct{}, 1),
		}

		c.setupNotificationDisconnectHandler(ctx, client)

		// Echo recycles the pooled context onto a new request while the watcher
		// is waiting for the disconnect.
		var wg sync.WaitGroup
		wg.Go(func() { ctx.SetRequest(recycledRequest()) })

		// Unblock the watcher (client disconnect); wait until it has signalled
		// and the recycle has finished.
		cancel()
		<-client.Done
		wg.Wait()

		// The context is recycled again while the watcher logs the disconnect.
		ctx.SetRequest(recycledRequest())

		// Wait for the watcher to finish.
		synctest.Wait()
	})
}
