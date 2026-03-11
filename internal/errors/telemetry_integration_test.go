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

func TestShouldReportToSentry_FiltersNoRouteToHost(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("dial tcp 192.168.1.1:1883: connect: no route to host")).
		Component("mqtt").
		Category(CategoryMQTTConnection).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_FiltersDNSErrors(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("dial tcp: lookup en.wikipedia.org: server misbehaving")).
		Component("imageprovider").
		Category(CategoryNetwork).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_FiltersConnectionRefused(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("dial tcp 10.0.0.1:8080: connection refused")).
		Component("weather").
		Category(CategoryHTTP).
		Build()
	assert.False(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_AllowsCodeErrors(t *testing.T) {
	t.Parallel()
	ee := New(fmt.Errorf("species not found in taxonomy")).
		Component("birdnet").
		Category(CategoryNotFound).
		Build()
	assert.True(t, shouldReportToSentry(ee))
}

func TestShouldReportToSentry_AllowsNetworkCategoryCodeBugs(t *testing.T) {
	t.Parallel()
	// Network category error that is NOT environmental noise should still report
	ee := New(fmt.Errorf("unexpected status code 500 from API")).
		Component("imageprovider").
		Category(CategoryNetwork).
		Build()
	assert.True(t, shouldReportToSentry(ee))
}
