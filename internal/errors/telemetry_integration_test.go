package errors

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TransportMock is a mock Sentry transport that captures events for inspection.
type TransportMock struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *TransportMock) Flush(_ time.Duration) bool              { return true }
func (t *TransportMock) FlushWithContext(_ context.Context) bool { return true }
func (t *TransportMock) Configure(_ sentry.ClientOptions)        {} //nolint:gocritic // hugeParam: signature required by sentry.Transport interface
func (t *TransportMock) Close()                                  {}
func (t *TransportMock) SendEvent(event *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event)
}

// Events returns the captured events.
func (t *TransportMock) Events() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.events
}

func TestReportError_IncludesStacktrace(t *testing.T) {
	transport := &TransportMock{}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:       "https://examplePublicKey@o0.ingest.sentry.io/0",
		Transport: transport,
	})
	require.NoError(t, err)

	reporter := NewSentryReporter(true)
	ee := New(fmt.Errorf("test error")).
		Component("test").
		Category(CategoryDatabase).
		Context("operation", "test_op").
		Build()

	reporter.ReportError(ee)
	sentry.Flush(2 * time.Second)

	require.NotEmpty(t, transport.Events(), "should have captured an event")
	event := transport.Events()[0]
	require.NotEmpty(t, event.Exception, "event should have exceptions")
	assert.NotNil(t, event.Exception[0].Stacktrace, "exception should have a stacktrace")
	assert.NotEmpty(t, event.Exception[0].Stacktrace.Frames, "stacktrace should have frames")
}
