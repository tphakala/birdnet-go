package notification

import (
	"context"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// fakeProvider implements PushProvider for testing
type fakeProvider struct {
	name    string
	enabled bool
	types   map[Type]bool
	recvCh  chan *Notification
}

func (f *fakeProvider) GetName() string          { return f.name }
func (f *fakeProvider) ValidateConfig() error    { return nil }
func (f *fakeProvider) SupportsType(t Type) bool { return f.types[t] }
func (f *fakeProvider) IsEnabled() bool          { return f.enabled }
func (f *fakeProvider) Send(ctx context.Context, n *Notification) error {
	select {
	case f.recvCh <- n:
	default:
	}
	return nil
}

func TestPushDispatcher_ForwardsNotification(t *testing.T) {
	// Ensure no global service initialized
	// Create isolated service for test
	svc := NewService(DefaultServiceConfig())
	if err := SetServiceForTesting(svc); err != nil {
		svc = GetService()
		if svc == nil {
			t.Fatalf("failed to attach to notification service: %v", err)
		}
	}

	// Setup fake provider that accepts all types
	fp := &fakeProvider{
		name:    "fake",
		enabled: true,
		types:   map[Type]bool{TypeError: true, TypeInfo: true, TypeWarning: true, TypeDetection: true, TypeSystem: true},
		recvCh:  make(chan *Notification, 1),
	}

	// Build dispatcher with fake provider
	d := &pushDispatcher{
		providers:      []registeredProvider{{prov: fp, filter: conf.PushFilterConfig{}, name: fp.name}},
		log:            getFileLogger(false),
		enabled:        true,
		maxRetries:     0,
		retryDelay:     10 * time.Millisecond,
		defaultTimeout: 200 * time.Millisecond,
	}

	if err := d.start(); err != nil {
		t.Fatalf("failed to start dispatcher: %v", err)
	}
	defer func() {
		if d.cancel != nil {
			d.cancel()
		}
	}()

	// Create a notification and expect provider to receive it
	_, err := svc.Create(TypeInfo, PriorityLow, "Hello", "World")
	if err != nil {
		t.Fatalf("create notification failed: %v", err)
	}

	select {
	case n := <-fp.recvCh:
		if n.Title != "Hello" || n.Message != "World" {
			t.Fatalf("received wrong notification: %+v", n)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for provider to receive notification")
	}
}
