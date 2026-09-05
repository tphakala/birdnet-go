// internal/api/v2/audio/streams_health_golden_test.go
package audio

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// TestConvertStreamHealthToResponse_Golden locks the health API JSON for every
// FFmpeg process state. The legacy process_state string the frontend switches on
// must stay byte-identical to the pre-seam output (the ffmpeg producer records
// its process name in StateDetail); the additive neutral state field carries the
// connection vocabulary. Building each health the way GetHealth does (mapped
// State plus StateDetail) exercises the exact production path.
func TestConvertStreamHealthToResponse_Golden(t *testing.T) {
	t.Parallel()

	const goldenURL = "rtsp://camera.local:554/stream"

	// goldenTemplate is the full serialized StreamHealthResponse for a stream
	// with no data, error, or history yet. Only process_state and state vary per
	// FFmpeg process state, so a change to any other field, its omitempty, or the
	// field order fails every row.
	goldenTemplate := `{"url":"` + goldenURL + `","is_healthy":false,"process_state":%q,"state":%q,"last_data_received":null,"restart_count":0,"total_bytes_received":0,"bytes_per_second":0,"is_receiving_data":false}`

	tests := []struct {
		name        string
		state       audiocore.StreamState
		detail      string // legacy process_state name the ffmpeg producer records
		wantProcess string
		wantState   string
	}{
		{"idle", audiocore.StreamStateStarting, "idle", "idle", "starting"},
		{"starting", audiocore.StreamStateStarting, "starting", "starting", "starting"},
		{"running", audiocore.StreamStateConnected, "running", "running", "connected"},
		{"restarting", audiocore.StreamStateReconnecting, "restarting", "restarting", "reconnecting"},
		{"backoff", audiocore.StreamStateReconnecting, "backoff", "backoff", "reconnecting"},
		{"circuit_open", audiocore.StreamStateReconnecting, "circuit_open", "circuit_open", "reconnecting"},
		{"stopped", audiocore.StreamStateStopped, "stopped", "stopped", "stopped"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			health := &audiocore.StreamHealth{State: tt.state, StateDetail: tt.detail}
			response := convertStreamHealthToResponse(goldenURL, health)

			// Field-level guards make a failure legible before the byte compare.
			assert.Equal(t, tt.wantProcess, response.ProcessState, "legacy process_state must be byte-identical")
			assert.Equal(t, tt.wantState, response.State, "neutral state field")

			got, err := json.Marshal(response)
			require.NoError(t, err)
			want := fmt.Sprintf(goldenTemplate, tt.wantProcess, tt.wantState)
			// Compare the exact marshaled bytes, not a semantic JSON match, so a
			// reordered field or changed whitespace (which assert.JSONEq tolerates)
			// also fails the byte-identity contract this test exists to lock.
			assert.Equal(t, want, string(got))
		})
	}
}

// TestRemovedStreamHealth_ByteIdentity pins the synthetic health the
// stream_removed SSE event carries. The legacy process_state must stay "idle"
// byte-identically (the pre-seam empty ffmpeg.StreamHealth defaulted to the idle
// process state), while the additive neutral state field reports "stopped" for a
// stream that is gone. The literals are asserted directly (not via the source
// constant) so a regression that drops StateDetail or changes the constant is
// caught here rather than silently passing.
func TestRemovedStreamHealth_ByteIdentity(t *testing.T) {
	t.Parallel()

	health := removedStreamHealth()
	response := convertStreamHealthToResponse("rtsp://camera.local:554/stream", &health)

	assert.Equal(t, "idle", response.ProcessState, "removed-stream process_state must stay byte-identical")
	assert.Equal(t, "stopped", response.State, "removed-stream neutral state should read stopped")
}
