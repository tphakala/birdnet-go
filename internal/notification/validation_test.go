package notification

import (
	"testing"
	"time"
)

// TestSetDeduplicationWindowValidation tests the validation logic in SetDeduplicationWindow
func TestSetDeduplicationWindowValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		inputWindow    time.Duration
		expectedWindow time.Duration
	}{
		{
			name:           "positive duration is accepted",
			inputWindow:    10 * time.Minute,
			expectedWindow: 10 * time.Minute,
		},
		{
			name:           "zero duration uses default",
			inputWindow:    0,
			expectedWindow: DefaultDeduplicationWindow,
		},
		{
			name:           "negative duration uses default",
			inputWindow:    -5 * time.Minute,
			expectedWindow: DefaultDeduplicationWindow,
		},
		{
			name:           "very large duration is accepted",
			inputWindow:    24 * time.Hour,
			expectedWindow: 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewInMemoryStore(100)
			store.SetDeduplicationWindow(tt.inputWindow)
			
			// Access the deduplicationWindow directly since we're in the same package
			store.mu.RLock()
			actualWindow := store.deduplicationWindow
			store.mu.RUnlock()
			
			if actualWindow != tt.expectedWindow {
				t.Errorf("SetDeduplicationWindow(%v): expected window %v, got %v",
					tt.inputWindow, tt.expectedWindow, actualWindow)
			}
		})
	}
}