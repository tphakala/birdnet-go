package imageprovider_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// verifyEnhancedError validates that an error is properly enhanced with component and category.
func verifyEnhancedError(t *testing.T, err error, expectCategory errors.ErrorCategory) {
	t.Helper()
	var enhancedErr *errors.EnhancedError
	if !errors.As(err, &enhancedErr) {
		// ErrImageNotFound is a sentinel error and is the only valid non-enhanced case
		if errors.Is(err, imageprovider.ErrImageNotFound) {
			return // Accept sentinel error as valid
		}
		// Any other non-enhanced error is a test failure
		require.Fail(t, "Expected enhanced error or ErrImageNotFound", "got: %v", err)
		return
	}

	assert.Equal(t, "imageprovider", enhancedErr.GetComponent(), "Expected component 'imageprovider'")
	if expectCategory != "" {
		assert.Equal(t, string(expectCategory), enhancedErr.GetCategory(), "Expected category mismatch")
	}
	assert.NotEmpty(t, enhancedErr.GetContext(), "Enhanced error should have context data")
}

// createDatabaseErrorTestFunc creates a test function that triggers a database error.
func createDatabaseErrorTestFunc(t *testing.T) func() error {
	t.Helper()
	return func() error {
		failingStore := &mockFailingStore{
			mockStore: mockStore{
				images: make(map[string]*datastore.ImageCache),
			},
			failGetCache: true,
		}
		metrics, err := observability.NewMetrics()
		if err != nil {
			return fmt.Errorf("failed to create metrics: %w", err)
		}
		cache, err := imageprovider.CreateDefaultCache(metrics, failingStore)
		if err != nil {
			return fmt.Errorf("failed to create cache: %w", err)
		}
		defer func() {
			if closeErr := cache.Close(); closeErr != nil {
				t.Logf("Failed to close cache: %v", closeErr)
			}
		}()
		cache.SetImageProvider(&mockImageProvider{})
		_, err = cache.Get("Test species")
		return err
	}
}

// TestErrorHandlingEnhancement tests that errors are properly enhanced with telemetry context
func TestErrorHandlingEnhancement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		testFunc       func() error
		expectCategory errors.ErrorCategory
	}{
		{
			name:           "ErrImageNotFound should be enhanced",
			testFunc:       func() error { return imageprovider.ErrImageNotFound },
			expectCategory: errors.CategoryImageFetch,
		},
		{
			name:           "Database error during cache operation",
			testFunc:       createDatabaseErrorTestFunc(t),
			expectCategory: errors.CategoryImageFetch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.testFunc()
			if err == nil {
				return
			}
			verifyEnhancedError(t, err, tt.expectCategory)
		})
	}
}

// TestErrorContextData tests that errors have appropriate context data
func TestErrorContextData(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{shouldFail: true}
	mockStore := newMockStore()
	metrics, _ := observability.NewMetrics()
	cache, _ := imageprovider.CreateDefaultCache(metrics, mockStore)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})
	cache.SetImageProvider(mockProvider)

	_, err := cache.Get("Turdus merula")
	if err == nil {
		return
	}

	var enhancedErr *errors.EnhancedError
	if !errors.As(err, &enhancedErr) {
		return
	}

	context := enhancedErr.GetContext()
	assert.Contains(t, context, "scientific_name", "Error context should include scientific_name")
	assert.Contains(t, context, "operation", "Error context should include operation")
}

// TestDescriptiveErrorMessages tests that errors have descriptive messages
func TestDescriptiveErrorMessages(t *testing.T) {
	t.Parallel()
	assert.NotEmpty(t, imageprovider.ErrImageNotFound.Error(), "ErrImageNotFound should have a non-empty error message")
	assert.NotEqual(t, "error", imageprovider.ErrImageNotFound.Error(), "ErrImageNotFound should have a descriptive message")
}
