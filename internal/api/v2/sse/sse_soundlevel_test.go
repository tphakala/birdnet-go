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
	// authTestHeader marks a test request as authenticated for the stub auth check.
	authTestHeader = "X-Test-Authenticated"
	// soundLevelEventTimeout bounds how long a test waits for a sound level event.
	soundLevelEventTimeout = 5 * time.Second
	// soundLevelBroadcastRetry is the pause between broadcasts while a client registers.
	soundLevelBroadcastRetry = 20 * time.Millisecond
)

func newSoundLevelEvent(name string) apicore.SSESoundLevelData {
	var ev apicore.SSESoundLevelData
	ev.Timestamp = time.Unix(1700000000, 0)
	ev.Source = soundLevelTestSourceID
	ev.Name = name
	ev.Duration = 10
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
		input := newSoundLevelEvent(sensitiveDisplayName)

		out := soundLevelPayloadForClient(&input, false)

		assert.Equal(t, soundLevelTestSourceID, out.Name, "display name must not reach unauthenticated clients")
		assert.Equal(t, soundLevelTestSourceID, out.Source)
		assert.Equal(t, sensitiveDisplayName, input.Name, "shared broadcast struct must not be mutated")
		assert.Equal(t, input.OctaveBands, out.OctaveBands)
		assert.Equal(t, input.EventType, out.EventType)
	})

	t.Run("authenticated keeps the display name", func(t *testing.T) {
		t.Parallel()
		input := newSoundLevelEvent("Front Garden")

		out := soundLevelPayloadForClient(&input, true)

		assert.Equal(t, "Front Garden", out.Name)
		assert.Equal(t, soundLevelTestSourceID, out.Source)
	})
}

// openSoundLevelStream connects to the sound level stream and returns a channel
// of decoded sound level events. Events are delivered until the test ends.
func openSoundLevelStream(t *testing.T, serverURL string, authenticated bool) <-chan apicore.SSESoundLevelData {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, serverURL+"/api/v2/soundlevels/stream", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	if authenticated {
		req.Header.Set(authTestHeader, "1")
	}
	client := apitest.NewTestHTTPClient(soundLevelEventTimeout * 4)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	t.Cleanup(func() {
		_ = resp.Body.Close()
		client.CloseIdleConnections()
	})

	events := make(chan apicore.SSESoundLevelData, 16)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var event string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && event == "soundlevel":
				var data apicore.SSESoundLevelData
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data) == nil {
					events <- data
				}
			}
		}
	}()
	return events
}

// broadcastUntilReceived broadcasts the event repeatedly (the client registers
// asynchronously and full channels drop updates) until one event arrives.
func broadcastUntilReceived(t *testing.T, core *apicore.Core, events <-chan apicore.SSESoundLevelData, name string) apicore.SSESoundLevelData {
	t.Helper()
	deadline := time.After(soundLevelEventTimeout)
	for {
		ev := newSoundLevelEvent(name)
		core.SSEManager.BroadcastSoundLevel(&ev)
		select {
		case got, ok := <-events:
			require.True(t, ok, "stream closed before a sound level event arrived")
			if got.Name == name || got.Name == soundLevelTestSourceID {
				return got
			}
			// A stale event from an earlier broadcast; keep draining.
		case <-deadline:
			require.FailNow(t, "timed out waiting for a sound level event")
		case <-time.After(soundLevelBroadcastRetry):
		}
	}
}

func TestStreamSoundLevelsAnonymization(t *testing.T) {
	t.Parallel()

	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
	h := New(core, func(ctx echo.Context) bool { return ctx.Request().Header.Get(authTestHeader) == "1" })
	h.RegisterRoutes(core.Group)
	server := httptest.NewServer(e)
	t.Cleanup(func() {
		core.SSEManager.CloseAllClients()
		server.Close()
	})

	anon := openSoundLevelStream(t, server.URL, false)
	authed := openSoundLevelStream(t, server.URL, true)

	got := broadcastUntilReceived(t, core, anon, sensitiveDisplayName)
	assert.Equal(t, soundLevelTestSourceID, got.Name, "unauthenticated client must receive the source id, not the display name")
	assert.Equal(t, soundLevelTestSourceID, got.Source)

	got = broadcastUntilReceived(t, core, authed, "Front Garden")
	assert.Equal(t, "Front Garden", got.Name)

	// A rename is followed by authenticated clients and still hidden from
	// unauthenticated ones.
	got = broadcastUntilReceived(t, core, authed, "Back Garden")
	assert.Equal(t, "Back Garden", got.Name)
	got = broadcastUntilReceived(t, core, anon, "Back Garden")
	assert.Equal(t, soundLevelTestSourceID, got.Name)
}
