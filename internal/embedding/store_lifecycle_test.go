package embedding

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStorePersistsAcrossReopen verifies that data written to the database
// survives closing and reopening the store at the same path, and that
// reopening migrates cleanly against an existing schema.
func TestStorePersistsAcrossReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "embeddings.db")
	rec := makeRecord(t)

	s1, err := NewStore(path)
	require.NoError(t, err)
	require.NoError(t, s1.Put(t.Context(), rec))
	require.NoError(t, s1.Close())

	s2, err := NewStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s2.Close()) })

	got, err := s2.Get(t.Context(), rec.DetectionID)
	require.NoError(t, err)
	assert.Equal(t, rec.Vector, got.Vector, "data must survive reopen")
}

// TestStoreUsesSeparateFile verifies that the store writes to its own database
// file and creates the parent directory if needed, keeping it isolated from the
// main application database.
func TestStoreUsesSeparateFile(t *testing.T) {
	t.Parallel()

	// Nested directory that does not exist yet exercises parent-dir creation.
	path := filepath.Join(t.TempDir(), "nested", "embeddings.db")
	s, err := NewStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.NoError(t, s.Put(t.Context(), makeRecord(t)))

	info, err := os.Stat(path)
	require.NoError(t, err, "store must persist to its own file")
	assert.Positive(t, info.Size(), "database file must contain data")
}

// TestStorePutInt8Rejected verifies that the store surfaces the unsupported
// format error when asked to store an int8-encoded vector, which is gated until
// the offline separability probe validates it.
func TestStorePutInt8Rejected(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	rec := makeRecord(t, func(r *Record) { r.Format = FormatInt8 })

	err := s.Put(t.Context(), rec)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedFormat)
}

// TestStoreConcurrentPut verifies that concurrent writers do not race or trip
// "database is locked"; every distinct detection is stored exactly once.
func TestStoreConcurrentPut(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	const writers = 16

	errCh := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			rec := makeRecord(t, func(r *Record) {
				r.DetectionID = fmt.Sprintf("det-%02d", i)
			})
			errCh <- s.Put(t.Context(), rec)
		})
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err, "concurrent Put must not fail")
	}

	all, err := s.Query(t.Context(), "birdnet_v3", time.Time{}, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Len(t, all, writers, "every concurrent writer stored exactly one row")
}
