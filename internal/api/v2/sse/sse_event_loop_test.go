// sse_event_loop_test.go verifies the detection and sound level event loops
// block on their channels instead of polling: events are written as soon as they
// are broadcast (no poll interval elapses), heartbeats still go out, the loops
// exit on request cancellation and on client removal, and a pending disconnect
// wins over buffered events. The tests run in a synctest bubble, so time is
// virtual and synctest.Test fails if any loop goroutine is left running.
package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

const (
	// loopTestDetectionEvent, loopTestPendingEvent, loopTestSoundLevelEvent and
	// loopTestHeartbeatEvent are the SSE event lines the loops write.
	loopTestDetectionEvent  = "event: detection\n"
	loopTestPendingEvent    = "event: pending\n"
	loopTestSoundLevelEvent = "event: soundlevel\n"
	loopTestHeartbeatEvent  = "event: heartbeat\n"
	// loopTestConnectedEvent is the first message every stream writes.
	loopTestConnectedEvent = "event: " + apicore.SSEStatusConnected + "\n"
	// loopTestBufferedEvents is how many events are queued before a disconnect.
	loopTestBufferedEvents = 3
	// loopTestDisconnectRepeats repeats the disconnect-precedence check, since a
	// select picks among ready cases at random and one run could pass by luck.
	loopTestDisconnectRepeats = 50
)

// recordingWriter is an in-memory http.ResponseWriter and http.Flusher that
// keeps every write, so a test can inspect what a stream sent.
type recordingWriter struct {
	header http.Header
	mu     sync.Mutex
	writes []string
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{header: http.Header{}}
}

func (w *recordingWriter) Header() http.Header { return w.header }
func (w *recordingWriter) WriteHeader(int)     {}
func (w *recordingWriter) Flush()              {}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, string(p))
	return len(p), nil
}

// count returns how many writes start with the given SSE event line.
func (w *recordingWriter) count(eventLine string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, s := range w.writes {
		if strings.HasPrefix(s, eventLine) {
			n++
		}
	}
	return n
}

// loopTestStream is a running stream handler under test.
type loopTestStream struct {
	writer *recordingWriter
	cancel context.CancelFunc
	err    error
	done   chan struct{}
}

// startLoopTestStream runs a stream handler in a goroutine of the caller's
// synctest bubble and waits until it has sent its connection message and is
// blocked in its event loop.
func startLoopTestStream(t *testing.T, handler func(echo.Context) error, path string) *loopTestStream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody).WithContext(ctx)
	s := &loopTestStream{writer: newRecordingWriter(), cancel: cancel, done: make(chan struct{})}
	ectx := echo.New().NewContext(req, s.writer)
	go func() {
		defer close(s.done)
		s.err = handler(ectx)
	}()
	synctest.Wait()
	require.Equal(t, 1, s.writer.count(loopTestConnectedEvent), "stream must send its connection message")
	return s
}

// stop cancels the request and waits for the handler to return, so no stream
// goroutine (or bubble channel) outlives the synctest bubble even when an
// assertion fails early.
func (s *loopTestStream) stop() {
	s.cancel()
	<-s.done
}

// exited reports whether the handler has returned.
func (s *loopTestStream) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func newLoopTestHandler(t *testing.T) (*Handler, *apicore.Core) {
	t.Helper()
	core := apitest.NewCore(t, apitest.WithoutSettingsPublish())
	return New(core, func(echo.Context) bool { return false }), core
}

func TestDetectionLoopDeliversWithoutPolling(t *testing.T) {
	h, core := newLoopTestHandler(t)
	synctest.Test(t, func(t *testing.T) {
		s := startLoopTestStream(t, h.StreamDetections, detectionStreamEndpoint)
		defer s.stop()
		start := time.Now()

		core.SSEManager.BroadcastDetection(&apicore.SSEDetectionData{CommonName: "Eurasian Wren"})
		synctest.Wait()
		assert.Equal(t, 1, s.writer.count(loopTestDetectionEvent), "detection must be written without waiting")

		core.SSEManager.BroadcastPending([]processor.SSEPendingDetection{{Species: "Eurasian Wren"}})
		synctest.Wait()
		assert.Equal(t, 1, s.writer.count(loopTestPendingEvent), "pending snapshot must be written without waiting")

		assert.Zero(t, time.Since(start), "no virtual time may pass before delivery")

		s.cancel()
		synctest.Wait()
		require.True(t, s.exited())
		assert.NoError(t, s.err)
	})
}

func TestSoundLevelLoopDeliversWithoutPolling(t *testing.T) {
	h, core := newLoopTestHandler(t)
	synctest.Test(t, func(t *testing.T) {
		s := startLoopTestStream(t, h.StreamSoundLevels, soundLevelStreamEndpoint)
		defer s.stop()
		start := time.Now()

		ev := newSoundLevelEvent(sensitiveDisplayName, soundLevelTestDuration)
		core.SSEManager.BroadcastSoundLevel(&ev)
		synctest.Wait()
		assert.Equal(t, 1, s.writer.count(loopTestSoundLevelEvent), "sound level must be written without waiting")
		assert.Zero(t, time.Since(start), "no virtual time may pass before delivery")

		s.cancel()
		synctest.Wait()
		require.True(t, s.exited())
		assert.NoError(t, s.err)
	})
}

func TestEventLoopsSendHeartbeat(t *testing.T) {
	tests := []struct {
		name   string
		stream func(*Handler) func(echo.Context) error
		path   string
	}{
		{"detections", func(h *Handler) func(echo.Context) error { return h.StreamDetections }, detectionStreamEndpoint},
		{"sound levels", func(h *Handler) func(echo.Context) error { return h.StreamSoundLevels }, soundLevelStreamEndpoint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newLoopTestHandler(t)
			synctest.Test(t, func(t *testing.T) {
				s := startLoopTestStream(t, tt.stream(h), tt.path)
				defer s.stop()

				time.Sleep(apicore.SSEHeartbeatInterval - time.Nanosecond)
				synctest.Wait()
				assert.Zero(t, s.writer.count(loopTestHeartbeatEvent), "no heartbeat before the interval")

				time.Sleep(time.Nanosecond)
				synctest.Wait()
				assert.Equal(t, 1, s.writer.count(loopTestHeartbeatEvent), "heartbeat at the interval")

				time.Sleep(apicore.SSEHeartbeatInterval)
				synctest.Wait()
				assert.Equal(t, 2, s.writer.count(loopTestHeartbeatEvent), "heartbeat every interval")

			})
		})
	}
}

func TestEventLoopsExitOnClientRemoval(t *testing.T) {
	tests := []struct {
		name   string
		stream func(*Handler) func(echo.Context) error
		path   string
	}{
		{"detections", func(h *Handler) func(echo.Context) error { return h.StreamDetections }, detectionStreamEndpoint},
		{"sound levels", func(h *Handler) func(echo.Context) error { return h.StreamSoundLevels }, soundLevelStreamEndpoint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, core := newLoopTestHandler(t)
			synctest.Test(t, func(t *testing.T) {
				s := startLoopTestStream(t, tt.stream(h), tt.path)
				defer s.stop()

				core.SSEManager.CloseAllClients()
				synctest.Wait()
				require.True(t, s.exited(), "loop must exit when the client is removed")
				assert.NoError(t, s.err)
			})
		})
	}
}

// TestEventLoopsPreferDisconnectOverBufferedEvents checks that a loop whose
// client has already been removed returns without writing the events still
// buffered on its channels, as the polling loop did.
func TestEventLoopsPreferDisconnectOverBufferedEvents(t *testing.T) {
	h, _ := newLoopTestHandler(t)

	newEctx := func(w *recordingWriter) echo.Context {
		req := httptest.NewRequest(http.MethodGet, detectionStreamEndpoint, http.NoBody).WithContext(t.Context())
		return echo.New().NewContext(req, w)
	}

	t.Run("detections", func(t *testing.T) {
		for range loopTestDisconnectRepeats {
			client := createSSEClient("buffered-detection", apicore.StreamTypeDetections)
			client.Channel = make(chan apicore.SSEDetectionData, loopTestBufferedEvents)
			client.PendingChan = make(chan any, loopTestBufferedEvents)
			for range loopTestBufferedEvents {
				client.Channel <- apicore.SSEDetectionData{}
				client.PendingChan <- []processor.SSEPendingDetection{}
			}
			close(client.Done)

			w := newRecordingWriter()
			require.NoError(t, h.runSSEEventLoopMulti(newEctx(w), client, client.ID, detectionStreamEndpoint))
			require.Empty(t, w.writes)
		}
	})

	t.Run("sound levels", func(t *testing.T) {
		for range loopTestDisconnectRepeats {
			client := createSSEClient("buffered-soundlevel", apicore.StreamTypeSoundLevels)
			client.SoundLevelChan = make(chan apicore.SSESoundLevelData, loopTestBufferedEvents)
			for range loopTestBufferedEvents {
				client.SoundLevelChan <- apicore.SSESoundLevelData{}
			}
			close(client.Done)

			w := newRecordingWriter()
			err := runSSEEventLoop(h, newEctx(w), client, client.ID, soundLevelStreamEndpoint, client.SoundLevelChan,
				func(v *apicore.SSESoundLevelData) any { return v }, "soundlevel", apicore.StreamTypeSoundLevels)
			require.NoError(t, err)
			require.Empty(t, w.writes)
		}
	})
}

// TestEventLoopsExitOnClosedChannel checks that each loop returns when one of its
// data channels is closed while Done is still open, which is how a removed
// client looks to a loop that observes the channel before Done.
func TestEventLoopsExitOnClosedChannel(t *testing.T) {
	h, _ := newLoopTestHandler(t)

	newEctx := func(w *recordingWriter) echo.Context {
		req := httptest.NewRequest(http.MethodGet, detectionStreamEndpoint, http.NoBody).WithContext(t.Context())
		return echo.New().NewContext(req, w)
	}

	t.Run("detection channel", func(t *testing.T) {
		client := createSSEClient("closed-detection", apicore.StreamTypeDetections)
		client.Channel = make(chan apicore.SSEDetectionData)
		client.PendingChan = make(chan any)
		close(client.Channel)

		w := newRecordingWriter()
		require.NoError(t, h.runSSEEventLoopMulti(newEctx(w), client, client.ID, detectionStreamEndpoint))
		assert.Empty(t, w.writes)
	})

	t.Run("pending channel", func(t *testing.T) {
		client := createSSEClient("closed-pending", apicore.StreamTypeDetections)
		client.Channel = make(chan apicore.SSEDetectionData)
		client.PendingChan = make(chan any)
		close(client.PendingChan)

		w := newRecordingWriter()
		require.NoError(t, h.runSSEEventLoopMulti(newEctx(w), client, client.ID, detectionStreamEndpoint))
		assert.Empty(t, w.writes)
	})

	t.Run("sound level channel", func(t *testing.T) {
		client := createSSEClient("closed-soundlevel", apicore.StreamTypeSoundLevels)
		client.SoundLevelChan = make(chan apicore.SSESoundLevelData)
		close(client.SoundLevelChan)

		w := newRecordingWriter()
		err := runSSEEventLoop(h, newEctx(w), client, client.ID, soundLevelStreamEndpoint, client.SoundLevelChan,
			func(v *apicore.SSESoundLevelData) any { return v }, "soundlevel", apicore.StreamTypeSoundLevels)
		require.NoError(t, err)
		assert.Empty(t, w.writes)
	})
}

// TestDetectionLoopWithoutPendingChannel checks that a nil PendingChan, which a
// select never picks, does not stop detections from being delivered.
func TestDetectionLoopWithoutPendingChannel(t *testing.T) {
	h, _ := newLoopTestHandler(t)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, detectionStreamEndpoint, http.NoBody).WithContext(ctx)
		w := newRecordingWriter()

		client := createSSEClient("nil-pending", apicore.StreamTypeDetections)
		client.Channel = make(chan apicore.SSEDetectionData, 1)
		client.Channel <- apicore.SSEDetectionData{CommonName: "Eurasian Wren"}

		done := make(chan error, 1)
		go func() {
			done <- h.runSSEEventLoopMulti(echo.New().NewContext(req, w), client, client.ID, detectionStreamEndpoint)
		}()
		synctest.Wait()
		assert.Equal(t, 1, w.count(loopTestDetectionEvent), "detection must be written with no pending channel")

		cancel()
		synctest.Wait()
		require.NoError(t, <-done)
	})
}
