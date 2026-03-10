package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// testHelper provides common assertion helpers for configuration tests.
// These helpers reduce code duplication and improve test readability.

// requireEnhancedError asserts that the error is an EnhancedError and returns it.
func requireEnhancedError(t *testing.T, err error) *errors.EnhancedError {
	t.Helper()
	require.Error(t, err)
	var enhanced *errors.EnhancedError
	require.ErrorAs(t, err, &enhanced, "expected EnhancedError type, got %T", err)
	return enhanced
}

// assertValidationError asserts that an error is an EnhancedError with
// CategoryValidation and the specified validation type in context.
func assertValidationError(t *testing.T, err error, validationType string) {
	t.Helper()
	enhanced := requireEnhancedError(t, err)
	assert.Equal(t, errors.CategoryValidation, enhanced.Category,
		"expected CategoryValidation, got %s", enhanced.Category)

	if validationType != "" {
		ctx, exists := enhanced.Context["validation_type"]
		assert.True(t, exists, "expected validation_type context to be set")
		assert.Equal(t, validationType, ctx,
			"expected validation_type = %s, got %s", validationType, ctx)
	}
}
