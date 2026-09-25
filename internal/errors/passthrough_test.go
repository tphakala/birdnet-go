package errors

import (
	"fmt"
	"io/fs"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// passthroughTestError is a value-receiver error type used to check that
// AsType matches non-pointer error types in a wrapped chain.
type passthroughTestError struct{ code int }

func (e passthroughTestError) Error() string { return fmt.Sprintf("code %d", e.code) }

func TestAsType(t *testing.T) {
	t.Parallel()

	pathErr := &fs.PathError{Op: "open", Path: "/tmp/x", Err: fs.ErrNotExist}

	t.Run("matches pointer type through fmt wrapping", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("outer: %w", pathErr)

		got, ok := AsType[*fs.PathError](wrapped)
		require.True(t, ok)
		assert.Same(t, pathErr, got)
	})

	t.Run("matches through EnhancedError", func(t *testing.T) {
		t.Parallel()
		enhanced := New(pathErr).
			Component("test").
			Category(CategoryFileIO).
			Build()

		got, ok := AsType[*fs.PathError](enhanced)
		require.True(t, ok)
		assert.Same(t, pathErr, got)
	})

	t.Run("finds EnhancedError itself in the chain", func(t *testing.T) {
		t.Parallel()
		enhanced := New(NewStd("boom")).
			Component("test").
			Category(CategoryValidation).
			Build()
		wrapped := fmt.Errorf("context: %w", enhanced)

		got, ok := AsType[*EnhancedError](wrapped)
		require.True(t, ok)
		assert.Equal(t, CategoryValidation, got.Category)
	})

	t.Run("matches value type through Join", func(t *testing.T) {
		t.Parallel()
		joined := Join(NewStd("first"), passthroughTestError{code: 7})

		got, ok := AsType[passthroughTestError](joined)
		require.True(t, ok)
		assert.Equal(t, 7, got.code)
	})

	t.Run("no match returns zero value and false", func(t *testing.T) {
		t.Parallel()
		got, ok := AsType[*fs.PathError](NewStd("plain"))
		assert.False(t, ok)
		assert.Nil(t, got)
	})

	t.Run("nil error returns false", func(t *testing.T) {
		t.Parallel()
		got, ok := AsType[*fs.PathError](nil)
		assert.False(t, ok)
		assert.Nil(t, got)
	})

	t.Run("matches interface type parameter", func(t *testing.T) {
		t.Parallel()
		enhanced := New(NewStd("boom")).
			Component("test").
			Category(CategoryNotFound).
			Build()
		wrapped := fmt.Errorf("context: %w", enhanced)

		got, ok := AsType[interface {
			error
			GetCategory() string
		}](wrapped)
		require.True(t, ok)
		assert.Equal(t, string(CategoryNotFound), got.GetCategory())
	})
}

func TestErrUnsupportedPassthrough(t *testing.T) {
	t.Parallel()

	// net/http's ErrNotSupported reports itself as the standard library's
	// errors.ErrUnsupported and nothing else, so this fails unless the
	// passthrough is that exact sentinel rather than a lookalike.
	require.ErrorIs(t, http.ErrNotSupported, ErrUnsupported)

	wrapped := fmt.Errorf("feature x: %w", ErrUnsupported)
	require.ErrorIs(t, wrapped, ErrUnsupported)
}

func TestIsCategory(t *testing.T) {
	t.Parallel()

	notFound := New(NewStd("missing")).
		Component("test").
		Category(CategoryNotFound).
		Build()

	tests := []struct {
		name     string
		err      error
		category ErrorCategory
		want     bool
	}{
		{name: "matching category", err: notFound, category: CategoryNotFound, want: true},
		{name: "different category", err: notFound, category: CategoryValidation, want: false},
		{name: "wrapped enhanced error", err: fmt.Errorf("outer: %w", notFound), category: CategoryNotFound, want: true},
		{name: "plain error", err: NewStd("plain"), category: CategoryNotFound, want: false},
		{name: "nil error", err: nil, category: CategoryNotFound, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsCategory(tt.err, tt.category))
		})
	}
}
