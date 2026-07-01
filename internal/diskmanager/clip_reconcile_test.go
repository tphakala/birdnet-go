package diskmanager

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
)

// --- test helpers ---

// fakeReconcileStore is a small in-memory ReconcileStore for the crawler tests.
type fakeReconcileStore struct {
	refs     []ClipReference // ordered by ID ascending
	cleared  []string        // clip_names passed to ClearNoteClipPathsByNames
	getErr   error
	clearErr error
}

func (f *fakeReconcileStore) GetNoteClipReferences(afterID uint, limit int) ([]ClipReference, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := make([]ClipReference, 0, limit)
	for _, r := range f.refs {
		if r.ID <= afterID {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeReconcileStore) ClearNoteClipPathsByNames(clipNames []string) (int64, error) {
	if f.clearErr != nil {
		return 0, f.clearErr
	}
	f.cleared = append(f.cleared, clipNames...)
	return int64(len(clipNames)), nil
}

// writeClip creates baseDir/rel (with parent dirs) so it stats as present.
func writeClip(t *testing.T, baseDir, rel string) {
	t.Helper()
	full := filepath.Join(baseDir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte("audio"), 0o600))
}

// withNoChunkPause disables the inter-chunk sleep for a test and restores it after.
func withNoChunkPause(t *testing.T) {
	t.Helper()
	orig := reconcileChunkPause
	reconcileChunkPause = 0
	t.Cleanup(func() { reconcileChunkPause = orig })
}

const testOld = -time.Hour // completion time well outside ClipRecencyWindow

// --- evaluateClipChunk tests ---

func TestEvaluateClipChunk(t *testing.T) {
	t.Parallel()

	now := time.Now()
	baseDir := t.TempDir()
	// present.wav exists on disk; the others do not.
	writeClip(t, baseDir, "2024/01/present.wav")
	// A fresh encoding temp for encoding.wav (final file absent).
	require.NoError(t, os.WriteFile(
		filepath.Join(baseDir, "2024", "01", "encoding.wav"+audiotemp.Ext),
		[]byte("temp"), 0o600))

	refs := []ClipReference{
		{ID: 1, ClipName: "2024/01/present.wav", CompletionTime: now.Add(testOld)},
		{ID: 2, ClipName: "2024/01/ghost.wav", CompletionTime: now.Add(testOld)},
		{ID: 3, ClipName: "2024/01/recent.wav", CompletionTime: now},          // within window -> skipped
		{ID: 4, ClipName: "2024/01/unknown.wav", CompletionTime: time.Time{}}, // zero ts -> skipped
		{ID: 5, ClipName: "2024/01/encoding.wav", CompletionTime: now.Add(testOld)},
		{ID: 6, ClipName: "../../etc/passwd", CompletionTime: now.Add(testOld)}, // traversal -> skipped
	}

	res := evaluateClipChunk(baseDir, refs, now)

	// present + encoding -> positive evidence storage is attached.
	assert.Equal(t, 2, res.positiveCount, "present.wav and encoding.wav are positive evidence")
	// only ghost.wav is a confirmed orphan.
	assert.Equal(t, []string{"2024/01/ghost.wav"}, res.orphans)
}

func TestEvaluateClipChunk_StaleTempIsOrphan(t *testing.T) {
	t.Parallel()

	now := time.Now()
	baseDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "2024", "01"), 0o750))
	tempPath := filepath.Join(baseDir, "2024", "01", "stale.wav"+audiotemp.Ext)
	require.NoError(t, os.WriteFile(tempPath, []byte("temp"), 0o600))
	// Age the temp beyond reconcileEncodingMaxAge so it no longer counts as encoding.
	old := now.Add(-time.Hour)
	require.NoError(t, os.Chtimes(tempPath, old, old))

	refs := []ClipReference{
		{ID: 1, ClipName: "2024/01/stale.wav", CompletionTime: now.Add(testOld)},
	}
	res := evaluateClipChunk(baseDir, refs, now)

	assert.Zero(t, res.positiveCount, "a stale temp is not an active encode")
	assert.Equal(t, []string{"2024/01/stale.wav"}, res.orphans)
}

// --- ReconcileClipOrphansPass tests ---

func TestReconcileClipOrphansPass_ClearsOrphansWithPositiveEvidence(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeClip(t, baseDir, "2024/01/present.wav") // positive evidence storage attached

	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2024/01/present.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 2, ClipName: "2024/01/ghost.wav", CompletionTime: time.Now().Add(testOld)},
	}}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, baseDir)

	assert.False(t, result.Aborted, "should complete: storage evidence present")
	assert.Equal(t, int64(1), result.Cleared)
	assert.Equal(t, []string{"2024/01/ghost.wav"}, store.cleared)
}

func TestReconcileClipOrphansPass_DetachedStorageGuardAborts(t *testing.T) {
	withNoChunkPause(t)

	// baseDir exists but is empty: mimics a cleanly-unmounted volume where every
	// clip resolves as missing. The pass must abort and clear nothing.
	baseDir := t.TempDir()

	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2024/01/a.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 2, ClipName: "2024/01/b.wav", CompletionTime: time.Now().Add(testOld)},
	}}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, baseDir)

	assert.True(t, result.Aborted)
	assert.Equal(t, "no attached-storage evidence", result.AbortReason)
	assert.Empty(t, store.cleared, "must clear nothing when storage looks detached")
	assert.Zero(t, result.Cleared)
}

func TestReconcileClipOrphansPass_DirectoryPresentGuard(t *testing.T) {
	withNoChunkPause(t)

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2024/01/a.wav", CompletionTime: time.Now().Add(testOld)},
	}}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, missing)

	assert.True(t, result.Aborted)
	assert.Equal(t, "export directory inaccessible", result.AbortReason)
	assert.Empty(t, store.cleared)
}

func TestReconcileClipOrphansPass_EmptyBaseDirAborts(t *testing.T) {
	withNoChunkPause(t)

	store := &fakeReconcileStore{}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, "   ")

	assert.True(t, result.Aborted)
	assert.Equal(t, "export path not configured", result.AbortReason)
}

func TestReconcileClipOrphansPass_RecentRowsNotCleared(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeClip(t, baseDir, "2024/01/present.wav") // positive evidence

	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2024/01/present.wav", CompletionTime: time.Now().Add(testOld)},
		// Missing file but recent -> may still be encoding -> must NOT be cleared.
		{ID: 2, ClipName: "2024/01/recent.wav", CompletionTime: time.Now()},
	}}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, baseDir)

	assert.False(t, result.Aborted)
	assert.Empty(t, store.cleared, "recent missing clip must not be cleared")
	assert.Zero(t, result.Cleared)
}

func TestReconcileClipOrphansPass_KeysetPaginationAcrossChunks(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()

	// More refs than one chunk to exercise keyset pagination and termination.
	// Present files are interspersed (even IDs) so every chunk has positive
	// evidence and the detached-storage guard never trips; odd IDs are ghosts.
	refs := make([]ClipReference, 0, reconcileChunkSize+50)
	wantCleared := 0
	for i := uint(1); i <= reconcileChunkSize+50; i++ {
		rel := filepath.ToSlash(filepath.Join("2024", "01", "clip"+strconv.FormatUint(uint64(i), 10)+".wav"))
		refs = append(refs, ClipReference{ID: i, ClipName: rel, CompletionTime: time.Now().Add(testOld)})
		if i%2 == 0 {
			writeClip(t, baseDir, rel) // present
		} else {
			wantCleared++ // ghost
		}
	}
	store := &fakeReconcileStore{refs: refs}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, baseDir)

	assert.False(t, result.Aborted, "no chunk is all-orphan, so the pass must complete")
	assert.Equal(t, len(refs), result.Scanned, "must scan every ref across chunks")
	assert.Equal(t, int64(wantCleared), result.Cleared)
}

// TestReconcileClipOrphansPass_AbortsMidPassOnAllOrphanChunk documents the safety
// property: a contiguous run of orphans large enough to fill a whole chunk (no
// present file) aborts the pass even after earlier chunks cleared successfully.
// This is the deliberate fail-safe against storage detaching mid-pass (an
// unmounted volume presents every subsequent clip as missing). The cost is that a
// very large contiguous ghost run is left untouched rather than risk data loss.
func TestReconcileClipOrphansPass_AbortsMidPassOnAllOrphanChunk(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()

	// Chunk 1 (IDs 1..chunkSize): a present file gives positive evidence, rest are
	// orphans -> cleared. Chunk 2 (IDs chunkSize+1..+50): all orphans -> abort.
	refs := make([]ClipReference, 0, reconcileChunkSize+50)
	writeClip(t, baseDir, "present.wav")
	refs = append(refs, ClipReference{ID: 1, ClipName: "present.wav", CompletionTime: time.Now().Add(testOld)})
	for i := uint(2); i <= reconcileChunkSize+50; i++ {
		rel := filepath.ToSlash(filepath.Join("2024", "01", "ghost"+strconv.FormatUint(uint64(i), 10)+".wav"))
		refs = append(refs, ClipReference{ID: i, ClipName: rel, CompletionTime: time.Now().Add(testOld)})
	}
	store := &fakeReconcileStore{refs: refs}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, baseDir)

	assert.True(t, result.Aborted, "second, all-orphan chunk must abort the pass")
	assert.Equal(t, "no attached-storage evidence", result.AbortReason)
	// Chunk 1's orphans were cleared before the abort; chunk 2's were not.
	assert.Equal(t, int64(reconcileChunkSize-1), result.Cleared)
}

func TestReconcileClipOrphansPass_HonorsQuitChannel(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeClip(t, baseDir, "present.wav")
	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "present.wav", CompletionTime: time.Now().Add(testOld)},
	}}
	quit := make(chan struct{})
	close(quit) // already shutting down

	result := ReconcileClipOrphansPass(quit, store, baseDir)

	assert.True(t, result.Aborted)
	assert.Equal(t, "shutdown", result.AbortReason)
	assert.Empty(t, store.cleared)
}

// --- isExportTempName tests (mirrors media.isExportTempFor) ---

func TestIsExportTempName(t *testing.T) {
	t.Parallel()

	base := "clip.wav"
	tests := []struct {
		name string
		file string
		want bool
	}{
		{"pre-fix temp", "clip.wav" + audiotemp.Ext, true},
		{"unique temp", "clip.wav.1234.5" + audiotemp.Ext, true},
		{"final file", "clip.wav", false},
		{"other clip temp", "other.wav" + audiotemp.Ext, false},
		{"non-numeric pid", "clip.wav.abc.5" + audiotemp.Ext, false},
		{"missing seq", "clip.wav.1234" + audiotemp.Ext, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isExportTempName(tt.file, base))
		})
	}
}
