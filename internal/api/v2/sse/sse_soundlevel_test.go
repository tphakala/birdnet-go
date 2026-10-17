// sse_soundlevel_test.go verifies the per-client source anonymization applied to
// the public sound level stream: unauthenticated subscribers must receive only
// the stable source id as the name, never the display name, while authenticated
// subscribers keep the display name (and follow renames). The shared broadcast
// struct must never be mutated.
package sse

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
)

const (
	// soundLevelTestSourceID is the stable source id a sound level event carries.
	soundLevelTestSourceID = "rtsp-1"
	// soundLevelTestDisplayName and soundLevelTestRenamed are display names an
	// authenticated subscriber sees before and after a rename.
	soundLevelTestDisplayName = "Front Garden"
	soundLevelTestRenamed     = "Back Garden"
	// authTestHeader marks a test request as authenticated for the stub auth check.
	authTestHeader = "X-Test-Authenticated"
	// authTestHeaderValue is the authTestHeader value that means authenticated.
	authTestHeaderValue = "1"
	// soundLevelEventName is the SSE event name the sound level stream uses.
	soundLevelEventName = "soundlevel"
	// sseEventPrefix and sseDataPrefix start the SSE lines the reader parses.
	sseEventPrefix = "event: "
	sseDataPrefix  = "data: "
	// soundLevelEventTimeout bounds how long a test waits for a sound level event.
	soundLevelEventTimeout = 5 * time.Second
	// soundLevelClientTimeoutFactor scales the HTTP client timeout over the wait bound.
	soundLevelClientTimeoutFactor = 4
	// soundLevelBroadcastRetry is the pause between broadcasts while a client registers.
	soundLevelBroadcastRetry = 20 * time.Millisecond
	// soundLevelReaderBuffer is the capacity of the decoded event channel.
	soundLevelReaderBuffer = 16
	// soundLevelScannerInitialBuffer and soundLevelScannerMaxBuffer size the SSE line scanner.
	soundLevelScannerInitialBuffer = 64 * 1024
	soundLevelScannerMaxBuffer     = 1024 * 1024
	// soundLevelTestDuration is the duration carried by every test event.
	soundLevelTestDuration = 10
)

// newSoundLevelEvent builds a test event. The marker is carried in Duration so a
// test can tell one broadcast from another even when the anonymized name is the
// same for all of them.
func newSoundLevelEvent(name string, marker int) apicore.SSESoundLevelData {
	var ev apicore.SSESoundLevelData
	ev.Timestamp = time.Unix(1700000000, 0)
	ev.Source = soundLevelTestSourceID
	ev.Name = name
	ev.Duration = marker
	ev.OctaveBands = map[string]soundlevel.OctaveBandData{
		"1000.0": {CenterFreq: 1000, Min: -60, Max: -40, Mean: -50},
	}
	ev.EventType = "sound_level_update"
	return ev
}

func TestSoundLevelPayloadForClient(t *testing.T) {
	t.Parallel()

	t.Run("unauthenticated gets the source id as name without mutating input", func(t *testing.T) {
		t.Parallel()
		input := newSoundLevelEvent(sensitiveDisplayName, soundLevelTestDuration)

		out := soundLevelPayloadForClient(&input, false)

		assert.Equal(t, soundLevelTestSourceID, out.Name, "display name must not reach unauthenticated clients")
		assert.Equal(t, soundLevelTestSourceID, out.Source)
		assert.Equal(t, sensitiveDisplayName, input.Name, "shared broadcast struct must not be mutated")
		assert.Equal(t, input.OctaveBands, out.OctaveBands)
		assert.Equal(t, input.EventType, out.EventType)
	})

	t.Run("authenticated keeps the display name", func(t *testing.T) {
		t.Parallel()
		input := newSoundLevelEvent(soundLevelTestDisplayName, soundLevelTestDuration)

		out := soundLevelPayloadForClient(&input, true)

		assert.Equal(t, soundLevelTestDisplayName, out.Name)
		assert.Equal(t, soundLevelTestSourceID, out.Source)
	})

	t.Run("unauthenticated with an empty or id-equal display name gets the id", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{"", soundLevelTestSourceID} {
			input := newSoundLevelEvent(name, soundLevelTestDuration)
			out := soundLevelPayloadForClient(&input, false)
			assert.Equal(t, soundLevelTestSourceID, out.Name)
		}
	})
}

// soundLevelStream is a connected sound level subscriber under test.
type soundLevelStream struct {
	events <-chan apicore.SSESoundLevelData
}

// newSoundLevelServer serves the SSE routes with the given auth check and returns
// the core (to broadcast on) and the server URL.
func newSoundLevelServer(t *testing.T, authCheck func(echo.Context) bool) (core *apicore.Core, serverURL string) {
	t.Helper()
	e := echo.New()
	core = apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
	h := New(core, authCheck)
	h.RegisterRoutes(core.Group)
	server := httptest.NewServer(e)
	t.Cleanup(func() {
		core.SSEManager.CloseAllClients()
		server.Close()
	})
	return core, server.URL
}

// headerAuthCheck authenticates a request that carries authTestHeader.
func headerAuthCheck(ctx echo.Context) bool {
	return ctx.Request().Header.Get(authTestHeader) == authTestHeaderValue
}

// openSoundLevelStream connects to the sound level stream and decodes its sound
// level events. The reader stops when the test ends, even with undrained events.
func openSoundLevelStream(t *testing.T, serverURL string, authenticated bool) soundLevelStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, serverURL+soundLevelStreamEndpoint, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	if authenticated {
		req.Header.Set(authTestHeader, authTestHeaderValue)
	}
	client := apitest.NewTestHTTPClient(soundLevelEventTimeout * soundLevelClientTimeoutFactor)
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = resp.Body.Close()
		client.CloseIdleConnections()
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	events := make(chan apicore.SSESoundLevelData, soundLevelReaderBuffer)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, soundLevelScannerInitialBuffer), soundLevelScannerMaxBuffer)
		var event string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, sseEventPrefix):
				event = strings.TrimPrefix(line, sseEventPrefix)
			case strings.HasPrefix(line, sseDataPrefix) && event == soundLevelEventName:
				var data apicore.SSESoundLevelData
				if json.Unmarshal([]byte(strings.TrimPrefix(line, sseDataPrefix)), &data) != nil {
					continue
				}
				select {
				case events <- data:
				case <-done:
					return
				}
			}
		}
	}()
	return soundLevelStream{events: events}
}

// broadcastMarker numbers broadcasts so each wait matches exactly its own event.
var broadcastMarker atomic.Int64

// broadcastUntilReceived broadcasts an event with a fresh marker repeatedly (the
// client registers asynchronously and a full channel drops updates) until the
// stream delivers an event carrying that marker, and returns it. Events left over
// from earlier broadcasts are skipped.
func broadcastUntilReceived(t *testing.T, core *apicore.Core, stream soundLevelStream, name string) apicore.SSESoundLevelData {
	t.Helper()
	marker := int(broadcastMarker.Add(1))
	deadline := time.After(soundLevelEventTimeout)
	for {
		ev := newSoundLevelEvent(name, marker)
		core.SSEManager.BroadcastSoundLevel(&ev)
		select {
		case got, ok := <-stream.events:
			require.True(t, ok, "stream closed before a sound level event arrived")
			if got.Duration == marker {
				return got
			}
		case <-deadline:
			require.FailNow(t, "timed out waiting for a sound level event")
		case <-time.After(soundLevelBroadcastRetry):
		}
	}
}

func TestStreamSoundLevelsAnonymization(t *testing.T) {
	t.Parallel()

	core, serverURL := newSoundLevelServer(t, headerAuthCheck)
	anon := openSoundLevelStream(t, serverURL, false)
	authed := openSoundLevelStream(t, serverURL, true)

	got := broadcastUntilReceived(t, core, anon, sensitiveDisplayName)
	assert.Equal(t, soundLevelTestSourceID, got.Name, "unauthenticated client must receive the source id, not the display name")
	assert.Equal(t, soundLevelTestSourceID, got.Source)

	got = broadcastUntilReceived(t, core, authed, soundLevelTestDisplayName)
	assert.Equal(t, soundLevelTestDisplayName, got.Name)

	// A rename is followed by authenticated clients and still hidden from
	// unauthenticated ones.
	got = broadcastUntilReceived(t, core, authed, soundLevelTestRenamed)
	assert.Equal(t, soundLevelTestRenamed, got.Name)
	got = broadcastUntilReceived(t, core, anon, soundLevelTestRenamed)
	assert.Equal(t, soundLevelTestSourceID, got.Name)
}

// TestStreamSoundLevelsNilAuthCheckFailsClosed pins that a handler without an
// auth check serves the source id, not the display name, on the real stream.
func TestStreamSoundLevelsNilAuthCheckFailsClosed(t *testing.T) {
	t.Parallel()

	core, serverURL := newSoundLevelServer(t, nil)
	stream := openSoundLevelStream(t, serverURL, true)

	got := broadcastUntilReceived(t, core, stream, sensitiveDisplayName)
	assert.Equal(t, soundLevelTestSourceID, got.Name)
}

// TestStreamSoundLevelsAuthCapturedAtConnect pins that the auth state is read once
// when the stream connects: a later change of the auth result does not alter what
// an open stream sends.
func TestStreamSoundLevelsAuthCapturedAtConnect(t *testing.T) {
	t.Parallel()

	var authenticated atomic.Bool
	authenticated.Store(true)
	core, serverURL := newSoundLevelServer(t, func(echo.Context) bool { return authenticated.Load() })
	stream := openSoundLevelStream(t, serverURL, false)

	got := broadcastUntilReceived(t, core, stream, soundLevelTestDisplayName)
	require.Equal(t, soundLevelTestDisplayName, got.Name)

	authenticated.Store(false)
	got = broadcastUntilReceived(t, core, stream, soundLevelTestRenamed)
	assert.Equal(t, soundLevelTestRenamed, got.Name, "an open stream keeps its connect-time auth state")
}
