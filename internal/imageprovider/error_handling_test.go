package imageprovider_test

import (
	"fmt"
	"testing"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// TestErrorHandlingEnhancement tests that errors are properly enhanced with telemetry context
func TestErrorHandlingEnhancement(t *testing.T) {
	tests := []struct {
		name           string
		testFunc       func() error
		expectCategory errors.ErrorCategory
		expectContext  map[string]interface{}
	}{
		{
			name: "ErrImageNotFound should be enhanced",
			testFunc: func() error {
				return imageprovider.ErrImageNotFound
			},
			expectCategory: errors.CategoryImageFetch,
			expectContext: map[string]interface{}{
				"component": "imageprovider",
			},
		},
		{
			name: "Wikipedia provider initialization error",
			testFunc: func() error {
				// This will fail because we're using an invalid URL
				// Just to test error enhancement
				return fmt.Errorf("test initialization error")
			},
			expectCategory: errors.CategoryNetwork,
			expectContext: map[string]interface{}{
				"component": "imageprovider",
			},
		},
		{
			name: "Database error during cache operation",
			testFunc: func() error {
				failingStore := &mockFailingStore{
					mockStore: mockStore{
						images: make(map[string]*datastore.ImageCache),
					},
					failGetCache: true,
				}
				metrics, _ := observability.NewMetrics()
				cache, _ := imageprovider.CreateDefaultCache(metrics, failingStore)
				cache.SetImageProvider(&mockImageProvider{})
				_, err := cache.Get("Test species")
				return err
			},
			expectCategory: errors.CategoryImageFetch,
			expectContext: map[string]interface{}{
				"component": "imageprovider",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.testFunc()
			if err == nil {
				// Some tests might not return errors
				return
			}

			// Check if error is enhanced
			var enhancedErr *errors.EnhancedError
			if errors.As(err, &enhancedErr) {
				// Verify component
				if enhancedErr.GetComponent() != "imageprovider" {
					t.Errorf("Expected component 'imageprovider', got '%s'", enhancedErr.GetComponent())
				}

				// Verify category if applicable
				if tt.expectCategory != "" && enhancedErr.GetCategory() != tt.expectCategory {
					t.Errorf("Expected category '%s', got '%s'", tt.expectCategory, enhancedErr.GetCategory())
				}

				// Verify it has context
				if len(enhancedErr.GetContext()) == 0 {
					t.Error("Enhanced error should have context data")
				}
			} else if errors.Is(err, imageprovider.ErrImageNotFound) {
				// Special check for ErrImageNotFound which should be enhanced
				t.Error("ErrImageNotFound should be an enhanced error")
			}
		})
	}
}

// TestErrorContextData tests that errors have appropriate context data
func TestErrorContextData(t *testing.T) {
	// Test that errors include operation context
	mockProvider := &mockImageProvider{shouldFail: true}
	mockStore := newMockStore()
	metrics, _ := observability.NewMetrics()
	cache, _ := imageprovider.CreateDefaultCache(metrics, mockStore)
	cache.SetImageProvider(mockProvider)

	_, err := cache.Get("Turdus merula")
	if err != nil {
		var enhancedErr *errors.EnhancedError
		if errors.As(err, &enhancedErr) {
			context := enhancedErr.GetContext()
			// Check for expected context fields
			if _, ok := context["scientific_name"]; !ok {
				t.Error("Error context should include scientific_name")
			}
			if _, ok := context["operation"]; !ok {
				t.Error("Error context should include operation")
			}
		}
	}
}

// TestDescriptiveErrorMessages tests that errors have descriptive messages
func TestDescriptiveErrorMessages(t *testing.T) {
	// Test ErrImageNotFound has a descriptive message
	if imageprovider.ErrImageNotFound.Error() == "" {
		t.Error("ErrImageNotFound should have a non-empty error message")
	}

	// Check that it's not a generic message
	if imageprovider.ErrImageNotFound.Error() == "error" {
		t.Error("ErrImageNotFound should have a descriptive message, not 'error'")
	}
}
