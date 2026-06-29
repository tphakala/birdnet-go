package api

import "github.com/tphakala/birdnet-go/internal/api/v2/apicore"

// Type aliases re-export the shared types that moved to the apicore package so
// existing handlers and tests in package api keep referring to them by their
// short names. These are zero-cost aliases (identical types), not wrappers.
type (
	// ErrorResponse is the API error response structure (defined in apicore).
	ErrorResponse = apicore.ErrorResponse
	// SSEDetectionData is the sanitized detection wire struct sent over SSE.
	SSEDetectionData = apicore.SSEDetectionData
	// SSESoundLevelData is the sound-level wire struct sent over SSE.
	SSESoundLevelData = apicore.SSESoundLevelData
	// SSESourceInfo describes the audio source in SSE events.
	SSESourceInfo = apicore.SSESourceInfo
	// SSEBirdImage is the bird image payload in SSE detection events.
	SSEBirdImage = apicore.SSEBirdImage
	// SSEManager manages SSE connections and broadcasts.
	SSEManager = apicore.SSEManager
	// SSEClient is a single connected SSE client.
	SSEClient = apicore.SSEClient
	// ShutdownRequester triggers a programmatic shutdown.
	ShutdownRequester = apicore.ShutdownRequester
)

// SSE stream scaffolding re-declared from apicore so the SSE contract and
// dashboard-public-endpoint tests still resident in package api keep referring to
// them by their short names (the SSE producers themselves now live in the sse,
// audio, notifications and imports domains). The canonical definitions and the
// SendSSEHeartbeat/LogSSEConnection/SendConnectionMessage helpers live in apicore
// and are promoted onto *Controller via the embedded *apicore.Core.
const (
	// maxSSEStreamDuration is the maximum lifetime of a single SSE stream.
	maxSSEStreamDuration = apicore.MaxSSEStreamDuration
	// SSEStatusConnected marks an established SSE client connection.
	SSEStatusConnected = apicore.SSEStatusConnected
	// SSEStatusDisconnected marks a torn-down SSE client connection.
	SSEStatusDisconnected = apicore.SSEStatusDisconnected
)
