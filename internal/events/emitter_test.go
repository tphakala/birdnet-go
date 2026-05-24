package events

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore records calls for verification.
type fakeStore struct {
	calls    []fakeCall
	failNext bool
}

type fakeCall struct {
	category  string
	eventType string
	message   string
	metadata  map[string]any
}

func (f *fakeStore) SaveAppEvent(_ context.Context, category, eventType, message string, metadata map[string]any) error {
	f.calls = append(f.calls, fakeCall{category, eventType, message, metadata})
	if f.failNext {
		f.failNext = false
		return assert.AnError
	}
	return nil
}

func TestEmitter_Emit(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	e := NewEmitter(store)

	e.Emit(t.Context(), "system", "startup", "app started", map[string]any{"version": "1.0"})

	require.Len(t, store.calls, 1)
	assert.Equal(t, "system", store.calls[0].category)
	assert.Equal(t, "startup", store.calls[0].eventType)
	assert.Equal(t, "app started", store.calls[0].message)
	assert.Equal(t, "1.0", store.calls[0].metadata["version"])
}

func TestEmitter_NilSafety(t *testing.T) {
	t.Parallel()

	var nilEmitter *Emitter
	assert.NotPanics(t, func() {
		nilEmitter.Emit(t.Context(), "system", "startup", "test", nil)
	}, "Emit on nil Emitter must not panic")

	emitterNoStore := &Emitter{}
	assert.NotPanics(t, func() {
		emitterNoStore.Emit(t.Context(), "system", "startup", "test", nil)
	}, "Emit with nil store must not panic")
}

func TestEmitter_ErrorSwallowed(t *testing.T) {
	t.Parallel()
	store := &fakeStore{failNext: true}
	e := NewEmitter(store)

	assert.NotPanics(t, func() {
		e.Emit(t.Context(), "system", "startup", "test", nil)
	}, "Emit must not propagate store errors")

	require.Len(t, store.calls, 1, "store should still have been called")
}

func TestPackageLevelEmit_NoDefault(t *testing.T) {
	old := defaultEmitter.Load()
	defaultEmitter.Store(nil)
	t.Cleanup(func() { defaultEmitter.Store(old) })

	assert.NotPanics(t, func() {
		Emit(t.Context(), "system", "startup", "test", nil)
	}, "package-level Emit must not panic when Default is nil")
}

func TestSetDefaultAndDefault(t *testing.T) {
	old := defaultEmitter.Load()
	t.Cleanup(func() { defaultEmitter.Store(old) })

	store := &fakeStore{}
	e := NewEmitter(store)
	SetDefault(e)

	loaded := Default()
	require.NotNil(t, loaded)
	loaded.Emit(t.Context(), "test", "ping", "hello", nil)

	require.Len(t, store.calls, 1)
	assert.Equal(t, "test", store.calls[0].category)
}

func TestEmitter_NilMetadata(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	e := NewEmitter(store)

	e.Emit(t.Context(), "system", "startup", "test", nil)

	require.Len(t, store.calls, 1)
	assert.Nil(t, store.calls[0].metadata)
}
