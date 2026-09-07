package notifications

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

// TestSetupNotificationDisconnectHandler_NoContextRaceAfterHandlerReturn is a
// regression test for issue #4292 ("fatal error: concurrent map read and map
// write"). The disconnect watcher goroutine outlives the SSE handler, and Echo
// pools and recycles the echo.Context once the handler returns, rebinding its
// *http.Request to the next in-flight request. Reading ctx (ctx.Request(),
// ctx.RealIP()) from the watcher therefore races with the request the pooled
// context is next bound to.
//
// This test simulates that recycle by swapping the context's request (and
// writing its header map) from another goroutine while the watcher runs. Under
// -race it fails if the watcher touches ctx after setup; it passes because the
// watcher captures the request context and client IP up front and never reads
// ctx afterwards.
func TestSetupNotificationDisconnectHandler_NoContextRaceAfterHandlerReturn(t *testing.T) {
	t.Parallel()

	c := mockHandler()

	e := echo.New()
	req, cancel := requestWithCancelCtx(t, "1.1.1.1:1111")
	defer cancel()
	ctx := e.NewContext(req, httptest.NewRecorder())

	client := &NotificationClient{
		ID:   "race-client",
		Done: make(chan struct{}, 1),
	}

	c.setupNotificationDisconnectHandler(ctx, client)

	// Simulate Echo recycling the pooled context onto new in-flight requests
	// while the watcher goroutine is running. Only this goroutine touches ctx;
	// a correctly-fixed watcher never does, so there is no race. A regressed
	// watcher reading ctx here trips the race detector.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			next := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			next.Header.Set(echo.HeaderXForwardedFor, "2.2.2.2")
			next.RemoteAddr = "2.2.2.2:2222"
			ctx.SetRequest(next)
		}
	})

	// Unblock the watcher (client disconnect) so its post-disconnect logging
	// (the ctx access in the pre-fix code) overlaps with the swapping goroutine.
	cancel()

	select {
	case <-client.Done:
		// Watcher observed the disconnect and is about to log.
	case <-time.After(2 * time.Second):
		close(stop)
		wg.Wait()
		t.Fatal("disconnect watcher did not signal client.Done")
	}

	// Keep swapping briefly so the watcher's logging step overlaps the swaps,
	// widening the window the race detector can observe if the bug regresses.
	time.Sleep(10 * time.Millisecond)
	close(stop)
	wg.Wait()
}
