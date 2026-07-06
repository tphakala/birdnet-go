// log_redaction_test.go: Regression tests for request/error log redaction.
// Ported from upstream commit ed93de6 (#3727); adapted to the pre-extraction
// package api layout (Controller/routePattern instead of apicore.Core/RoutePattern).
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// recordedEntry captures one logger call so tests can assert on the emitted
// structured fields.
type recordedEntry struct {
	level  logger.LogLevel
	msg    string
	fields []logger.Field
}

// recordingLogger is a logger.Logger that records every logged entry in memory
// so a test can assert on the structured fields that the middleware and error
// handler emit. It is safe for concurrent use even though the code under test
// logs synchronously.
type recordingLogger struct {
	mu      sync.Mutex
	entries []recordedEntry
}

func newRecordingLogger() *recordingLogger { return &recordingLogger{} }

func (r *recordingLogger) record(level logger.LogLevel, msg string, fields ...logger.Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Copy the fields so a caller reusing its slice cannot mutate the record.
	captured := make([]logger.Field, len(fields))
	copy(captured, fields)
	r.entries = append(r.entries, recordedEntry{level: level, msg: msg, fields: captured})
}

func (r *recordingLogger) Module(string) logger.Logger { return r }
func (r *recordingLogger) Trace(msg string, fields ...logger.Field) {
	r.record(logger.LogLevelTrace, msg, fields...)
}
func (r *recordingLogger) Debug(msg string, fields ...logger.Field) {
	r.record(logger.LogLevelDebug, msg, fields...)
}
func (r *recordingLogger) Info(msg string, fields ...logger.Field) {
	r.record(logger.LogLevelInfo, msg, fields...)
}
func (r *recordingLogger) Warn(msg string, fields ...logger.Field) {
	r.record(logger.LogLevelWarn, msg, fields...)
}
func (r *recordingLogger) Error(msg string, fields ...logger.Field) {
	r.record(logger.LogLevelError, msg, fields...)
}
func (r *recordingLogger) With(...logger.Field) logger.Logger        { return r }
func (r *recordingLogger) WithContext(context.Context) logger.Logger { return r }
func (r *recordingLogger) Log(level logger.LogLevel, msg string, fields ...logger.Field) {
	r.record(level, msg, fields...)
}
func (r *recordingLogger) Flush() error { return nil }

// requireSingleEntry asserts exactly one entry was logged and returns it.
func (r *recordingLogger) requireSingleEntry(t *testing.T) recordedEntry {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.entries, 1, "expected exactly one logged entry")
	return r.entries[0]
}

// requireStringField returns the string value of the field with key, failing the
// test if the field is missing or not a string. Field keys are interned, but the
// interned value compares equal by content to the plain key string.
func requireStringField(t *testing.T, entry recordedEntry, key string) string {
	t.Helper()
	for _, f := range entry.fields {
		if f.Key == key {
			value, ok := f.Value.(string)
			require.Truef(t, ok, "field %q is not a string: %T", key, f.Value)
			return value
		}
	}
	require.Failf(t, "field not found", "no field with key %q in logged entry", key)
	return ""
}

func TestRoutePattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setPath  string
		wantPath string
	}{
		{
			name:     "matched route returns the pattern",
			setPath:  "/api/v2/audio/:id",
			wantPath: "/api/v2/audio/:id",
		},
		{
			name:     "tokenized HLS route returns the pattern not the token",
			setPath:  "/api/v2/streams/hls/t/:streamToken/playlist.m3u8",
			wantPath: "/api/v2/streams/hls/t/:streamToken/playlist.m3u8",
		},
		{
			name:     "unmatched route returns the placeholder",
			setPath:  "",
			wantPath: unmatchedRoutePattern,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/hls/t/SECRET-TOKEN/playlist.m3u8", http.NoBody)
			ctx := e.NewContext(req, httptest.NewRecorder())
			if tt.setPath != "" {
				ctx.SetPath(tt.setPath)
			}

			got := routePattern(ctx)

			assert.Equal(t, tt.wantPath, got)
			assert.NotContains(t, got, "SECRET-TOKEN", "the raw path token must never appear in the route pattern")
		})
	}
}

func TestScrubQueryForLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rawQuery    string
		wantAbsent  []string
		wantPresent []string
		wantEmpty   bool
	}{
		{
			name:      "empty query stays empty",
			rawQuery:  "",
			wantEmpty: true,
		},
		{
			name:        "plain token value is redacted",
			rawQuery:    "token=secretvalue123&foo=bar",
			wantAbsent:  []string{"secretvalue123"},
			wantPresent: []string{"[TOKEN]", "foo=bar"},
		},
		{
			name:        "url-encoded token value is redacted after decoding",
			rawQuery:    "token=ab%2Bcd1234567890",
			wantAbsent:  []string{"ab%2Bcd1234567890", "ab+cd1234567890", "1234567890"},
			wantPresent: []string{"[TOKEN]"},
		},
		{
			// A literal '+' in a base64 token must stay part of the token value:
			// decoding with url.QueryUnescape would turn it into a space and split
			// the value, leaking the tail past the scrubber. url.PathUnescape
			// preserves the '+', so the whole value is redacted.
			name:        "literal plus in token value is redacted not split on a space",
			rawQuery:    "token=ab+cd1234567890",
			wantAbsent:  []string{"ab+cd1234567890", "ab cd1234567890", "1234567890"},
			wantPresent: []string{"[TOKEN]"},
		},
		{
			name:        "api_key value is redacted",
			rawQuery:    "api_key=SUPERSECRETKEY123",
			wantAbsent:  []string{"SUPERSECRETKEY123"},
			wantPresent: []string{"[TOKEN]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scrubQueryForLog(tt.rawQuery)
			if tt.wantEmpty {
				assert.Empty(t, got)
				return
			}
			for _, absent := range tt.wantAbsent {
				assert.NotContainsf(t, got, absent, "secret %q must be scrubbed from the logged query", absent)
			}
			for _, present := range tt.wantPresent {
				assert.Contains(t, got, present)
			}
		})
	}
}

func TestLoggingMiddlewareRedactsPathAndQuery(t *testing.T) {
	t.Parallel()

	const (
		hlsToken = "SUPERSECRETHLSTOKEN"
		routePat = "/api/v2/streams/hls/t/:streamToken/playlist.m3u8"
	)

	rec := newRecordingLogger()
	c := &Controller{apiLogger: rec}

	e := echo.New()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/streams/hls/t/"+hlsToken+"/playlist.m3u8?token=ab%2Bcd1234567890&page=2",
		http.NoBody,
	)
	ctx := e.NewContext(req, httptest.NewRecorder())
	// Echo populates ctx.Path() with the matched route pattern after routing.
	// LoggingMiddleware logs after next(ctx), so the pattern is set by then; the
	// test simulates that post-routing state with SetPath.
	ctx.SetPath(routePat)

	handler := c.LoggingMiddleware()(func(echo.Context) error { return nil })
	require.NoError(t, handler(ctx))

	entry := rec.requireSingleEntry(t)

	pathValue := requireStringField(t, entry, "path")
	assert.Equal(t, routePat, pathValue)
	assert.NotContains(t, pathValue, hlsToken, "the HLS token must never appear in the logged path")

	queryValue := requireStringField(t, entry, "query")
	assert.NotContains(t, queryValue, "1234567890", "the query token value must be scrubbed")
	assert.NotContains(t, queryValue, "ab%2Bcd", "the raw url-encoded token must not be logged")
	assert.Contains(t, queryValue, "[TOKEN]")
}

func TestLoggingMiddlewareUnmatchedRouteUsesPlaceholder(t *testing.T) {
	t.Parallel()

	rec := newRecordingLogger()
	c := &Controller{apiLogger: rec}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/does/not/exist", http.NoBody)
	ctx := e.NewContext(req, httptest.NewRecorder())
	// No SetPath: ctx.Path() is empty, simulating an unmatched request.

	handler := c.LoggingMiddleware()(func(echo.Context) error { return nil })
	require.NoError(t, handler(ctx))

	entry := rec.requireSingleEntry(t)
	assert.Equal(t, unmatchedRoutePattern, requireStringField(t, entry, "path"))
}

func TestLoggingMiddlewareScrubsHandlerError(t *testing.T) {
	t.Parallel()

	rec := newRecordingLogger()
	c := &Controller{apiLogger: rec}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/audio", http.NoBody)
	ctx := e.NewContext(req, httptest.NewRecorder())
	ctx.SetPath("/api/v2/streams/:source")

	// A handler that returns a credential-bearing error directly (without routing
	// it through HandleError) must not leak the raw error into the request log.
	handler := c.LoggingMiddleware()(func(echo.Context) error {
		return errors.NewStd("failed to dial rtsp://user:pass@192.168.1.50:554/stream")
	})
	err := handler(ctx)
	require.Error(t, err, "the middleware must propagate the handler error unchanged")

	entry := rec.requireSingleEntry(t)
	errValue := requireStringField(t, entry, "error")
	assert.NotContains(t, errValue, "user:pass", "credentials in the handler error must be scrubbed")
	assert.NotContains(t, errValue, "192.168.1.50", "the IP in the handler error must be scrubbed")
}

func TestHandleErrorInternalScrubsErrorAndPath(t *testing.T) {
	t.Parallel()

	const (
		hlsToken   = "SUPERSECRETHLSTOKEN"
		routePat   = "/api/v2/streams/hls/t/:streamToken/playlist.m3u8"
		devMessage = "audio stream unavailable"
	)

	rec := newRecordingLogger()
	c := &Controller{apiLogger: rec}

	e := echo.New()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v2/streams/hls/t/"+hlsToken+"/playlist.m3u8",
		http.NoBody,
	)
	ctx := e.NewContext(req, httptest.NewRecorder())
	ctx.SetPath(routePat)

	// Use a credential-bearing underlying error. Status 400 keeps the >=500
	// telemetry path (reportErrorToTelemetry) out of this unit test.
	credErr := errors.NewStd("failed to dial rtsp://user:pass@192.168.1.50:554/stream")
	require.NoError(t, c.handleErrorInternal(ctx, credErr, devMessage, http.StatusBadRequest, "", nil))

	entry := rec.requireSingleEntry(t)

	errValue := requireStringField(t, entry, "error")
	assert.NotContains(t, errValue, "user:pass", "credentials in the error must be scrubbed")
	assert.NotContains(t, errValue, "192.168.1.50", "the IP in the error must be scrubbed")

	pathValue := requireStringField(t, entry, "path")
	assert.Equal(t, routePat, pathValue)
	assert.NotContains(t, pathValue, hlsToken, "the HLS token must never appear in the logged path")

	// The developer-supplied message stays as-is (it is already sanitized).
	assert.Equal(t, devMessage, requireStringField(t, entry, "message"))
}
