package diskmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// --- test helpers ---

// fakeReconcileStore is a small in-memory ReconcileStore for the crawler tests.
type fakeReconcileStore struct {
	refs     []ClipReference // ordered by ID ascending
	cleared  []string        // clip_names passed to ClearNoteClipPathsByNames
	retained []string        // clip_names passed to RetainNoteSpectrogramsByClipNames
	getErr   error
	clearErr error
	// retainErr makes RetainNoteSpectrogramsByClipNames fail.
	retainErr error
	// reads records every GetNoteClipReferences call.
	reads []readCall
	// onGet, when set, runs before each read is served so a test can mutate disk or
	// settings between chunks.
	onGet func(afterID uint)
}

// readCall is one recorded GetNoteClipReferences call.
type readCall struct {
	afterID uint
	limit   int
}

// dropRefs removes the rows whose clip_name is in names, like the real store
// which no longer lists a row once its clip_name is cleared or moved.
func (f *fakeReconcileStore) dropRefs(names []string) {
	gone := make(map[string]bool, len(names))
	for _, n := range names {
		gone[n] = true
	}
	kept := f.refs[:0:0]
	for _, r := range f.refs {
		if !gone[r.ClipName] {
			kept = append(kept, r)
		}
	}
	f.refs = kept
}

func (f *fakeReconcileStore) GetNoteClipReferences(afterID uint, limit int) ([]ClipReference, error) {
	f.reads = append(f.reads, readCall{afterID: afterID, limit: limit})
	if f.onGet != nil {
		f.onGet(afterID)
	}
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
	f.dropRefs(clipNames)
	return int64(len(clipNames)), nil
}

func (f *fakeReconcileStore) RetainNoteSpectrogramsByClipNames(clipNames []string) (int64, error) {
	if f.retainErr != nil {
		return 0, f.retainErr
	}
	f.retained = append(f.retained, clipNames...)
	// Like the real store, moved rows leave the clip_name listing.
	f.dropRefs(clipNames)
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

	root, err := os.OpenRoot(baseDir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	res := evaluateClipChunk(root, refs, now, true)

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
	root, err := os.OpenRoot(baseDir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	res := evaluateClipChunk(root, refs, now, true)

	assert.Zero(t, res.positiveCount, "a stale temp is not an active encode")
	assert.Equal(t, []string{"2024/01/stale.wav"}, res.orphans)

	// Without earlier evidence the same orphan is held, never cleared.
	held := evaluateClipChunk(root, refs, now, false)
	assert.Empty(t, held.orphans)
	assert.Equal(t, 1, held.heldNoEvidence)
}

// TestEvaluateClipChunk_SymlinkEscapeIsIndeterminate verifies that a persisted
// clip_name resolving (via a symlink) outside the export root is treated as
// indeterminate: os.Root rejects the escape, so it is neither counted as
// attached-storage evidence nor cleared as an orphan.
func TestEvaluateClipChunk_SymlinkEscapeIsIndeterminate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	baseDir := t.TempDir()
	outside := t.TempDir()
	external := filepath.Join(outside, "external.wav")
	require.NoError(t, os.WriteFile(external, []byte("audio"), 0o600))

	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "2024", "01"), 0o750))
	link := filepath.Join(baseDir, "2024", "01", "escape.wav")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	refs := []ClipReference{
		{ID: 1, ClipName: "2024/01/escape.wav", CompletionTime: now.Add(testOld)},
	}
	root, err := os.OpenRoot(baseDir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	res := evaluateClipChunk(root, refs, now, true)

	assert.Zero(t, res.positiveCount, "escaping symlink is not attached-storage evidence")
	assert.Empty(t, res.orphans, "escaping symlink must not be cleared as an orphan")
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

	result := ReconcileClipOrphansPass(quit, store, baseDir, nil)

	assert.False(t, result.Aborted, "should complete: storage evidence present")
	assert.Equal(t, int64(1), result.Cleared)
	assert.Equal(t, []string{"2024/01/ghost.wav"}, store.cleared)
}

func TestReconcileClipOrphansPass_DetachedStorageClearsNothing(t *testing.T) {
	withNoChunkPause(t)

	// baseDir exists but is empty: mimics a cleanly-unmounted volume where every
	// clip resolves as missing. The pass must clear nothing and hold every row.
	baseDir := t.TempDir()

	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2024/01/a.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 2, ClipName: "2024/01/b.wav", CompletionTime: time.Now().Add(testOld)},
	}}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Empty(t, store.cleared, "must clear nothing when storage looks detached")
	assert.Zero(t, result.Cleared)
	assert.Equal(t, len(store.refs), result.HeldNoEvidence)
}

func TestReconcileClipOrphansPass_DirectoryPresentGuard(t *testing.T) {
	withNoChunkPause(t)

	notADir := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

	tests := []struct {
		name     string
		baseDir  string
		wantWarn bool
	}{
		{name: "directory does not exist yet", baseDir: filepath.Join(t.TempDir(), "does-not-exist"), wantWarn: false},
		{name: "path is not a directory", baseDir: notADir, wantWarn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeReconcileStore{refs: []ClipReference{
				{ID: 1, ClipName: "2024/01/a.wav", CompletionTime: time.Now().Add(testOld)},
			}}
			var result ReconcileResult
			out := logtest.Capture(t, func() {
				result = ReconcileClipOrphansPass(make(chan struct{}), store, tt.baseDir, nil)
			})

			assert.True(t, result.Aborted)
			assert.Equal(t, abortReasonDirInaccessible, result.AbortReason)
			assert.Empty(t, store.cleared)
			if tt.wantWarn {
				assert.Contains(t, out, "level=WARN", "an unusable export path must be visible at the default log level")
				assert.Contains(t, out, "export directory is not accessible")
			} else {
				assert.NotContains(t, out, "level=WARN", "a directory that does not exist yet is the normal state of a fresh install")
			}
		})
	}
}

func TestReconcileClipOrphansPass_UnreadableDirectoryWarns(t *testing.T) {
	skipIfDirPermissionsNotEnforced(t)
	withNoChunkPause(t)

	baseDir := filepath.Join(t.TempDir(), "clips")
	require.NoError(t, os.MkdirAll(baseDir, 0o750))
	require.NoError(t, os.Chmod(baseDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(baseDir, 0o750) })
	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2024/01/a.wav", CompletionTime: time.Now().Add(testOld)},
	}}

	var result ReconcileResult
	out := logtest.Capture(t, func() {
		result = ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)
	})

	assert.True(t, result.Aborted)
	assert.Equal(t, abortReasonDirInaccessible, result.AbortReason)
	assert.Empty(t, store.cleared)
	assert.Empty(t, store.reads, "an unreadable export directory must stop the pass before any store read")
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, "export directory is not accessible")
}

func TestReconcileClipOrphansPass_EmptyBaseDirAborts(t *testing.T) {
	withNoChunkPause(t)

	store := &fakeReconcileStore{}
	quit := make(chan struct{})

	result := ReconcileClipOrphansPass(quit, store, "   ", nil)

	assert.True(t, result.Aborted)
	assert.Equal(t, abortReasonNotConfigured, result.AbortReason)
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

	result := ReconcileClipOrphansPass(quit, store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Empty(t, store.cleared, "recent missing clip must not be cleared")
	assert.Zero(t, result.Cleared)
}

func TestReconcileClipOrphansPass_KeysetPaginationAcrossChunks(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()

	// More refs than one chunk to exercise keyset pagination and termination.
	// Present files are interspersed (even IDs) so every chunk has positive
	// evidence; odd IDs are ghosts, and ID 1 is held for lack of earlier evidence.
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

	result := ReconcileClipOrphansPass(quit, store, baseDir, nil)

	assert.False(t, result.Aborted, "every chunk holds a present file, so the pass must complete")
	assert.Equal(t, len(refs), result.Scanned, "must scan every ref across chunks")
	// ID 1 is a ghost before any evidence (ID 2 is the first present file): held.
	assert.Equal(t, int64(wantCleared-1), result.Cleared)
	assert.Equal(t, 1, result.HeldNoEvidence)
	require.Len(t, store.reads, 3, "two chunks plus the empty terminating read")
	assert.Equal(t, uint(0), store.reads[0].afterID)
	assert.Equal(t, uint(reconcileChunkSize), store.reads[1].afterID)
}

// TestReconcileClipOrphansPass_ClearsAllOrphanChunkAfterEvidence pins that a
// contiguous run of orphans filling a whole chunk no longer aborts the pass once an
// earlier row proved the storage is attached and the month directory exists.
func TestReconcileClipOrphansPass_ClearsAllOrphanChunkAfterEvidence(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	mkDir(t, baseDir, "2024/01")

	// ID 1 is present (evidence); IDs 2..250 are ghosts spanning chunk 1 and 2.
	refs := make([]ClipReference, 0, reconcileChunkSize+50)
	writeClip(t, baseDir, "present.wav")
	refs = append(refs, ClipReference{ID: 1, ClipName: "present.wav", CompletionTime: time.Now().Add(testOld)})
	for i := uint(2); i <= reconcileChunkSize+50; i++ {
		rel := filepath.ToSlash(filepath.Join("2024", "01", "ghost"+strconv.FormatUint(uint64(i), 10)+".wav"))
		refs = append(refs, ClipReference{ID: i, ClipName: rel, CompletionTime: time.Now().Add(testOld)})
	}
	store := &fakeReconcileStore{refs: refs}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Equal(t, int64(reconcileChunkSize+49), result.Cleared)
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

	result := ReconcileClipOrphansPass(quit, store, baseDir, nil)

	assert.True(t, result.Aborted)
	assert.Equal(t, abortReasonShutdown, result.AbortReason)
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

// --- row-ordered evidence tests ---

// refRange builds refs with IDs first..last (inclusive) named
// <dir>/<prefix><id>.wav, all with an old completion time.
func refRange(first, last uint, dir, prefix string) []ClipReference {
	refs := make([]ClipReference, 0, last-first+1)
	for id := first; id <= last; id++ {
		refs = append(refs, ClipReference{
			ID:             id,
			ClipName:       dir + "/" + prefix + strconv.FormatUint(uint64(id), 10) + ".wav",
			CompletionTime: time.Now().Add(testOld),
		})
	}
	return refs
}

// writeRefs creates every ref's file under baseDir.
func writeRefs(t *testing.T, baseDir string, refs []ClipReference) {
	t.Helper()
	for _, r := range refs {
		writeClip(t, baseDir, r.ClipName)
	}
}

// mkDir creates baseDir/rel as an empty directory.
func mkDir(t *testing.T, baseDir, rel string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, filepath.FromSlash(rel)), 0o750))
}

// clearedSet returns the cleared names as a set.
func clearedSet(store *fakeReconcileStore) map[string]bool {
	out := make(map[string]bool, len(store.cleared))
	for _, n := range store.cleared {
		out[n] = true
	}
	return out
}

func TestReconcileClipOrphansPass_ClearsOrphanRunAfterEvidence(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	chunk0 := refRange(1, reconcileChunkSize, "2025/01", "p")
	orphans := refRange(reconcileChunkSize+1, 3*reconcileChunkSize, "2025/03", "o")
	chunk3 := refRange(3*reconcileChunkSize+1, 4*reconcileChunkSize, "2026/01", "n")
	writeRefs(t, baseDir, chunk0)
	writeRefs(t, baseDir, chunk3)
	mkDir(t, baseDir, "2025/03")

	refs := slices.Concat(chunk0, orphans, chunk3)
	store := &fakeReconcileStore{refs: refs}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Equal(t, int64(len(orphans)), result.Cleared)
	assert.Equal(t, len(refs), result.Scanned)
	assert.Zero(t, result.HeldNoEvidence)
	got := clearedSet(store)
	for _, o := range orphans {
		assert.True(t, got[o.ClipName], "orphan %d must be cleared", o.ID)
	}
	require.GreaterOrEqual(t, len(store.reads), 4)
	assert.Equal(t, uint(0), store.reads[0].afterID)
	for i := 1; i < 4; i++ {
		assert.Equal(t, uint(i*reconcileChunkSize), store.reads[i].afterID, "read %d must continue by keyset", i)
	}
}

func TestReconcileClipOrphansPass_HoldsOrphansBeforeFirstEvidence(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	prefix := refRange(1, 250, "2025/01", "o")
	present := refRange(251, 450, "2026/10", "p")
	tail := refRange(451, 460, "2026/10", "t")
	writeRefs(t, baseDir, present)
	store := &fakeReconcileStore{refs: slices.Concat(prefix, present, tail)}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Equal(t, 250, result.HeldNoEvidence)
	assert.Equal(t, int64(len(tail)), result.Cleared)
	got := clearedSet(store)
	for _, o := range prefix {
		assert.False(t, got[o.ClipName], "prefix row %d precedes all evidence and must be held", o.ID)
	}
	for _, o := range tail {
		assert.True(t, got[o.ClipName], "row %d follows evidence and must be cleared", o.ID)
	}
}

func TestReconcileClipOrphansPass_HoldsOldestOrphansEvenWhenMonthDirExists(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	mkDir(t, baseDir, "2025/01")
	prefix := refRange(1, 250, "2025/01", "o")
	present := refRange(251, 450, "2026/10", "p")
	writeRefs(t, baseDir, present)
	store := &fakeReconcileStore{refs: slices.Concat(prefix, present)}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Empty(t, store.cleared, "an existing directory never evidences the prefix")
	assert.Equal(t, 250, result.HeldNoEvidence)
}

func TestReconcileClipOrphansPass_HoldsMissingDirOrphansAfterEvidence(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	chunk0 := refRange(1, reconcileChunkSize, "2025/01", "p")
	missingDir := refRange(reconcileChunkSize+1, 2*reconcileChunkSize, "2025/02", "m")
	withDir := refRange(2*reconcileChunkSize+1, 3*reconcileChunkSize, "2025/03", "d")
	writeRefs(t, baseDir, chunk0)
	mkDir(t, baseDir, "2025/03")
	store := &fakeReconcileStore{refs: slices.Concat(chunk0, missingDir, withDir)}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Equal(t, reconcileChunkSize, result.HeldMissingDir)
	assert.Equal(t, int64(len(withDir)), result.Cleared)
	got := clearedSet(store)
	for _, m := range missingDir {
		assert.False(t, got[m.ClipName])
	}
}

func TestEvaluateClipChunk_MissingDirOrphanHeldInChunkWithEvidence(t *testing.T) {
	t.Parallel()

	now := time.Now()
	baseDir := t.TempDir()
	writeClip(t, baseDir, "2025/01/present.wav")
	refs := []ClipReference{
		{ID: 1, ClipName: "2025/01/present.wav", CompletionTime: now.Add(testOld)},
		{ID: 2, ClipName: "2025/02/gone.wav", CompletionTime: now.Add(testOld)},
		{ID: 3, ClipName: "2025/01/ghost.wav", CompletionTime: now.Add(testOld)},
	}
	root, err := os.OpenRoot(baseDir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()

	res := evaluateClipChunk(root, refs, now, false)

	assert.Equal(t, []string{"2025/01/ghost.wav"}, res.orphans)
	assert.Equal(t, 1, res.heldMissingDir)
	assert.Zero(t, res.heldNoEvidence)
}

func TestEvaluateClipChunk_RowOrderWithinChunk(t *testing.T) {
	t.Parallel()

	now := time.Now()
	baseDir := t.TempDir()
	writeClip(t, baseDir, "2025/01/present.wav")
	refs := []ClipReference{
		{ID: 1, ClipName: "2025/01/first.wav", CompletionTime: now.Add(testOld)},
		{ID: 2, ClipName: "2025/01/present.wav", CompletionTime: now.Add(testOld)},
		{ID: 3, ClipName: "2025/01/last.wav", CompletionTime: now.Add(testOld)},
	}
	root, err := os.OpenRoot(baseDir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()

	res := evaluateClipChunk(root, refs, now, false)
	assert.Equal(t, []string{"2025/01/last.wav"}, res.orphans)
	assert.Equal(t, 1, res.heldNoEvidence)
	assert.True(t, res.evidenceSeen)

	res = evaluateClipChunk(root, refs, now, true)
	assert.Equal(t, []string{"2025/01/first.wav", "2025/01/last.wav"}, res.orphans)
	assert.Zero(t, res.heldNoEvidence)
	assert.True(t, res.evidenceSeen)
}

func TestReconcileClipOrphansPass_MidPassDetachHoldsRemainingChunks(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	chunk0 := refRange(1, reconcileChunkSize, "2025/01", "p")
	later := refRange(reconcileChunkSize+1, 3*reconcileChunkSize, "2025/02", "o")
	writeRefs(t, baseDir, chunk0)
	mkDir(t, baseDir, "2025/02")

	store := &fakeReconcileStore{refs: slices.Concat(chunk0, later)}
	store.onGet = func(afterID uint) {
		if afterID == reconcileChunkSize {
			require.NoError(t, os.RemoveAll(filepath.Join(baseDir, "2025")))
		}
	}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Empty(t, store.cleared, "later chunks lost their month directory and must be held")
	assert.Equal(t, len(later), result.HeldMissingDir)
}

func TestReconcileClipOrphansPass_AbortsWhenExportStorageChanges(t *testing.T) {
	// Each case changes the export storage while the second chunk is read; the
	// check before that chunk's writes must stop the pass before any of its rows
	// are cleared, and the third chunk must never be read.
	tests := []struct {
		name string
		// skipOnWindows skips a case that renames a directory the pass holds open,
		// which Windows refuses.
		skipOnWindows bool
		// change alters the storage and returns the pathCurrent callback for the pass.
		change func(t *testing.T, baseDir string) (trigger func(), pathCurrent func() bool)
	}{
		{
			name:          "export directory replaced",
			skipOnWindows: true,
			change: func(t *testing.T, baseDir string) (func(), func() bool) {
				t.Helper()
				return func() {
					require.NoError(t, os.Rename(baseDir, baseDir+".old"))
					mkDir(t, baseDir, "2025/01")
				}, nil
			},
		},
		{
			name: "export path setting changes",
			change: func(t *testing.T, _ string) (func(), func() bool) {
				t.Helper()
				current := true
				return func() { current = false }, func() bool { return current }
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipOnWindows && runtime.GOOS == "windows" {
				t.Skip("Windows refuses to rename a directory the pass holds open")
			}
			withNoChunkPause(t)

			baseDir := filepath.Join(t.TempDir(), "clips")
			require.NoError(t, os.MkdirAll(baseDir, 0o750))
			chunk0 := refRange(1, reconcileChunkSize, "2025/01", "p")
			second := refRange(reconcileChunkSize+1, 2*reconcileChunkSize, "2025/01", "o")
			third := refRange(2*reconcileChunkSize+1, 3*reconcileChunkSize, "2025/01", "t")
			writeRefs(t, baseDir, chunk0)

			trigger, pathCurrent := tt.change(t, baseDir)
			store := &fakeReconcileStore{refs: slices.Concat(chunk0, second, third)}
			store.onGet = func(afterID uint) {
				if afterID == reconcileChunkSize {
					trigger()
				}
			}

			result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, pathCurrent)

			assert.True(t, result.Aborted)
			assert.Equal(t, abortReasonExportDirChanged, result.AbortReason)
			// The pass stops before the writes of the second chunk, so the third is never read.
			require.Len(t, store.reads, 2, "the pass must not read the chunk that follows the change")
			assert.Equal(t, uint(0), store.reads[0].afterID)
			assert.Equal(t, uint(reconcileChunkSize), store.reads[1].afterID)
			got := clearedSet(store)
			// The change lands while the second chunk is read, so its clears are
			// stopped by the check before the writes.
			for _, r := range second {
				assert.False(t, got[r.ClipName], "row %d follows the storage change and must not be cleared", r.ID)
			}
		})
	}
}

func TestReconcileClipOrphansPass_AbortsBeforeReadWhenStorageAlreadyChanged(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	refs := refRange(1, 3, "2025/01", "p")
	writeRefs(t, baseDir, refs)
	store := &fakeReconcileStore{refs: refs}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, func() bool { return false })

	assert.True(t, result.Aborted)
	assert.Equal(t, abortReasonExportDirChanged, result.AbortReason)
	assert.Empty(t, store.reads, "the check before the read must stop the pass before any store read")
	assert.Empty(t, store.cleared)
}

func TestReconcileClipOrphansPass_FlatNameOrphanNeedsChunkEvidence(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	chunk0 := refRange(1, reconcileChunkSize, "2025/01", "p")
	writeRefs(t, baseDir, chunk0)
	flat := func(first, last uint, prefix string) []ClipReference {
		refs := refRange(first, last, "x", prefix)
		for i := range refs {
			refs[i].ClipName = strings.TrimPrefix(refs[i].ClipName, "x/")
		}
		return refs
	}
	ghosts := flat(reconcileChunkSize+1, 2*reconcileChunkSize, "ghost")
	mixed := flat(2*reconcileChunkSize+1, 3*reconcileChunkSize, "mix")
	writeClip(t, baseDir, mixed[0].ClipName)

	store := &fakeReconcileStore{refs: slices.Concat(chunk0, ghosts, mixed)}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Equal(t, len(ghosts), result.HeldMissingDir, "a flat chunk with no present row is held")
	assert.Equal(t, int64(len(mixed)-1), result.Cleared)
	got := clearedSet(store)
	for _, g := range ghosts {
		assert.False(t, got[g.ClipName])
	}
}

func TestReconcileClipOrphansPass_RenderRowEnablesLaterClears(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeFileIn(t, baseDir, "2026/01/b_514px.png", "png")
	store := &fakeReconcileStore{refs: []ClipReference{
		{ID: 1, ClipName: "2026/01/a.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 2, ClipName: "2026/01/b.wav", CompletionTime: time.Now().Add(testOld)},
		{ID: 3, ClipName: "2026/01/c.wav", CompletionTime: time.Now().Add(testOld)},
	}}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.Equal(t, 1, result.HeldNoEvidence)
	assert.Equal(t, []string{"2026/01/b.wav"}, store.retained)
	assert.Equal(t, []string{"2026/01/c.wav"}, store.cleared)
}

// TestReconcileClipOrphansPass_TransitionsAcrossPasses runs a pass, mutates store
// and disk, and runs again. After every pass no row below that pass's first present
// row may have been cleared, and a further pass on unchanged state clears nothing.
func TestReconcileClipOrphansPass_TransitionsAcrossPasses(t *testing.T) {
	const month = "2025/01"
	type scenario struct {
		name string
		// setup returns the initial refs and writes the initial disk state.
		setup func(t *testing.T, baseDir string) []ClipReference
		// mutate changes disk and store between passes 1 and 2.
		mutate func(t *testing.T, baseDir string, store *fakeReconcileStore)
		// wantFirst and wantSecond are the cumulative cleared names after each pass.
		wantFirst, wantSecond []string
	}
	mk := func(id uint, name string) ClipReference {
		return ClipReference{ID: id, ClipName: month + "/" + name + ".wav", CompletionTime: time.Now().Add(testOld)}
	}
	name := func(n string) string { return month + "/" + n + ".wav" }
	base := func(t *testing.T, baseDir string) []ClipReference {
		t.Helper()
		mkDir(t, baseDir, month)
		writeClip(t, baseDir, name("p1"))
		return []ClipReference{mk(1, "o1"), mk(2, "p1"), mk(3, "o2")}
	}
	tests := []scenario{
		{
			name:       "rerun unchanged",
			setup:      base,
			mutate:     func(*testing.T, string, *fakeReconcileStore) {},
			wantFirst:  []string{name("o2")},
			wantSecond: []string{name("o2")},
		},
		{
			name:  "orphan added at a high id",
			setup: base,
			mutate: func(_ *testing.T, _ string, s *fakeReconcileStore) {
				s.refs = append(s.refs, mk(4, "o3"))
			},
			wantFirst:  []string{name("o2")},
			wantSecond: []string{name("o2"), name("o3")},
		},
		{
			name:  "oldest present file deleted with row kept",
			setup: base,
			mutate: func(t *testing.T, baseDir string, s *fakeReconcileStore) {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(baseDir, filepath.FromSlash(name("p1")))))
				writeClip(t, baseDir, name("p2"))
				s.refs = append(s.refs, mk(5, "p2"), mk(6, "o4"))
			},
			wantFirst:  []string{name("o2")},
			wantSecond: []string{name("o2"), name("o4")},
		},
		{
			name:  "whole tree removed between passes",
			setup: base,
			mutate: func(t *testing.T, baseDir string, _ *fakeReconcileStore) {
				t.Helper()
				require.NoError(t, os.RemoveAll(filepath.Join(baseDir, "2025")))
			},
			wantFirst:  []string{name("o2")},
			wantSecond: []string{name("o2")},
		},
		{
			name:  "tree replaced by empty directory with new clips",
			setup: base,
			mutate: func(t *testing.T, baseDir string, s *fakeReconcileStore) {
				t.Helper()
				require.NoError(t, os.RemoveAll(filepath.Join(baseDir, "2025")))
				writeClip(t, baseDir, "2026/01/n1.wav")
				s.refs = append(s.refs,
					ClipReference{ID: 7, ClipName: "2026/01/n1.wav", CompletionTime: time.Now().Add(testOld)},
					ClipReference{ID: 8, ClipName: "2026/01/n2.wav", CompletionTime: time.Now().Add(testOld)})
			},
			wantFirst:  []string{name("o2")},
			wantSecond: []string{name("o2"), "2026/01/n2.wav"},
		},
		{
			name:  "reattach after detach",
			setup: base,
			mutate: func(t *testing.T, baseDir string, s *fakeReconcileStore) {
				t.Helper()
				// Detach: nothing present. Pass 2 holds everything, then the volume returns.
				require.NoError(t, os.RemoveAll(filepath.Join(baseDir, "2025")))
				second := ReconcileClipOrphansPass(make(chan struct{}), s, baseDir, nil)
				assert.Empty(t, s.cleared[1:], "detached pass must clear nothing")
				assert.Equal(t, 2, second.HeldNoEvidence)
				writeClip(t, baseDir, name("p1"))
				mkDir(t, baseDir, month)
			},
			wantFirst:  []string{name("o2")},
			wantSecond: []string{name("o2")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withNoChunkPause(t)
			baseDir := t.TempDir()
			store := &fakeReconcileStore{refs: tt.setup(t, baseDir)}

			first := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)
			assert.False(t, first.Aborted)
			assert.Equal(t, tt.wantFirst, store.cleared)
			assert.NotContains(t, store.cleared, name("o1"), "row below the first present row must stay")

			tt.mutate(t, baseDir, store)
			second := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)
			assert.False(t, second.Aborted)
			assert.Equal(t, tt.wantSecond, store.cleared)
			assert.NotContains(t, store.cleared, name("o1"))

			before := len(store.cleared)
			third := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)
			assert.False(t, third.Aborted)
			assert.Len(t, store.cleared, before, "a rerun on unchanged state clears nothing")
		})
	}
}

func TestReconcileClipOrphansPass_ClearErrorCountsWriteError(t *testing.T) {
	withNoChunkPause(t)

	baseDir := t.TempDir()
	writeClip(t, baseDir, "2024/01/present.wav")
	store := &fakeReconcileStore{
		clearErr: errors.NewStd("db down"),
		refs: []ClipReference{
			{ID: 1, ClipName: "2024/01/present.wav", CompletionTime: time.Now().Add(testOld)},
			{ID: 2, ClipName: "2024/01/ghost.wav", CompletionTime: time.Now().Add(testOld)},
		},
	}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, baseDir, nil)

	assert.False(t, result.Aborted)
	assert.Equal(t, 1, result.WriteErrors)
	assert.Zero(t, result.Cleared)
	assert.Len(t, store.refs, 2, "rows stay in the store")
}

func TestReconcileClipOrphansPass_ReadErrorAborts(t *testing.T) {
	withNoChunkPause(t)

	store := &fakeReconcileStore{getErr: errors.NewStd("db down")}

	result := ReconcileClipOrphansPass(make(chan struct{}), store, t.TempDir(), nil)

	assert.True(t, result.Aborted)
	assert.Equal(t, abortReasonReadError, result.AbortReason)
	assert.Empty(t, store.cleared)
}
