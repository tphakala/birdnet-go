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
		providers:      []enhancedProvider{{prov: fp, circuitBreaker: nil, filter: conf.PushFilterConfig{}, name: fp.name}},
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

func TestMatchesProviderFilter_ConfidenceOperators(t *testing.T) {
	tests := []struct {
		name       string
		condition  string
		confidence float64
		expected   bool
	}{
		// Greater than
		{"gt_pass", ">0.8", 0.9, true},
		{"gt_fail", ">0.8", 0.7, false},
		{"gt_equal", ">0.8", 0.8, false},

		// Less than
		{"lt_pass", "<0.8", 0.7, true},
		{"lt_fail", "<0.8", 0.9, false},
		{"lt_equal", "<0.8", 0.8, false},

		// Greater than or equal
		{"gte_pass_greater", ">=0.8", 0.9, true},
		{"gte_pass_equal", ">=0.8", 0.8, true},
		{"gte_fail", ">=0.8", 0.7, false},

		// Less than or equal
		{"lte_pass_less", "<=0.8", 0.7, true},
		{"lte_pass_equal", "<=0.8", 0.8, true},
		{"lte_fail", "<=0.8", 0.9, false},

		// Equal (single =)
		{"eq_pass", "=0.8", 0.8, true},
		{"eq_fail", "=0.8", 0.7, false},

		// Equal (double ==)
		{"eq2_pass", "==0.8", 0.8, true},
		{"eq2_fail", "==0.8", 0.7, false},

		// Edge cases with whitespace
		{"whitespace", " >= 0.8 ", 0.8, true},
		{"no_space", ">=0.8", 0.8, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{
				MetadataFilters: map[string]interface{}{
					"confidence": tt.condition,
				},
			}

			notif := &Notification{
				Metadata: map[string]interface{}{
					"confidence": tt.confidence,
				},
			}

			result := MatchesProviderFilter(filter, notif, nil, "test-provider")
			if result != tt.expected {
				t.Errorf("condition %q with confidence %v: expected %v, got %v",
					tt.condition, tt.confidence, tt.expected, result)
			}
		})
	}
}

func TestMatchesProviderFilter_ConfidenceErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		condition interface{}
		metadata  map[string]interface{}
		expected  bool
	}{
		// Invalid operator formats
		{"invalid_operator", "~0.8", map[string]interface{}{"confidence": 0.8}, false},
		{"empty_condition", "", map[string]interface{}{"confidence": 0.8}, false},
		{"no_operator", "0.8", map[string]interface{}{"confidence": 0.8}, false},

		// Invalid values
		{"invalid_threshold", ">abc", map[string]interface{}{"confidence": 0.8}, false},
		{"non_string_condition", 0.8, map[string]interface{}{"confidence": 0.8}, false},

		// Missing or invalid confidence
		{"missing_confidence", ">0.8", map[string]interface{}{}, false},
		{"invalid_confidence_type", ">0.8", map[string]interface{}{"confidence": "invalid"}, false},
		{"nil_confidence", ">0.8", map[string]interface{}{"confidence": nil}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{
				MetadataFilters: map[string]interface{}{
					"confidence": tt.condition,
				},
			}

			notif := &Notification{
				Metadata: tt.metadata,
			}

			result := MatchesProviderFilter(filter, notif, nil, "test-provider")
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestMatchesProviderFilter_ConfidenceTypes(t *testing.T) {
	tests := []struct {
		name       string
		confidence interface{}
		expected   bool
	}{
		{"float64", 0.85, true},
		{"float32", float32(0.85), true},
		{"string_valid", "0.85", true},
		{"string_invalid", "invalid", false},
		{"int", 1, false}, // ints don't convert to float in toFloat()
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{
				MetadataFilters: map[string]interface{}{
					"confidence": ">0.8",
				},
			}

			notif := &Notification{
				Metadata: map[string]interface{}{
					"confidence": tt.confidence,
				},
			}

			result := MatchesProviderFilter(filter, notif, nil, "test-provider")
			if result != tt.expected {
				t.Errorf("confidence type %T with value %v: expected %v, got %v",
					tt.confidence, tt.confidence, tt.expected, result)
			}
		})
	}
}
