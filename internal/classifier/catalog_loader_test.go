package classifier

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetActiveCatalog restores the package-global catalog to its default (nil =>
// EmbeddedCatalog) after a test that mutates it. These tests must NOT run in
// parallel because they share the global activeCatalog.
func resetActiveCatalog(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { setActiveCatalog(nil) })
}

// idSet returns the set of entry IDs in a catalog slice.
func idSet(entries []CatalogEntry) map[string]bool {
	ids := make(map[string]bool, len(entries))
	for i := range entries {
		ids[entries[i].ID] = true
	}
	return ids
}

// customEntry is a minimal, valid catalog entry for tests.
func customEntry(id string) CatalogEntry {
	return CatalogEntry{
		ID:       id,
		Name:     "Custom " + id,
		Category: CategoryBird,
		Version:  "1.0",
		Files: []CatalogFile{
			{RemotePath: "model.onnx", LocalName: id + ".onnx", Role: RoleModel},
		},
	}
}

// readManifest reads and unmarshals the catalog file at path.
func readManifest(t *testing.T, path string) catalogManifest {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	var m catalogManifest
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// writeManifest writes a manifest file for tests.
func writeManifest(t *testing.T, path string, m catalogManifest) {
	t.Helper()
	data, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func TestLoadCatalog_SeedsWhenAbsent(t *testing.T) {
	resetActiveCatalog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, catalogFileName)

	require.NoError(t, LoadCatalog(dir))

	// File is created and round-trips to the embedded catalog.
	require.FileExists(t, path)
	m := readManifest(t, path)
	assert.Equal(t, catalogSchemaVersion, m.SchemaVersion)

	wantChecksum, err := catalogChecksum(EmbeddedCatalog)
	require.NoError(t, err)
	assert.Equal(t, wantChecksum, m.CatalogChecksum)
	assert.Equal(t, idSet(EmbeddedCatalog), idSet(m.Entries), "seeded entries must match embedded")

	// The active catalog reflects the embedded catalog.
	assert.Equal(t, idSet(EmbeddedCatalog), idSet(ActiveCatalog()))
}

func TestLoadCatalog_LoadsFileWithCustomEntry(t *testing.T) {
	resetActiveCatalog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, catalogFileName)

	checksum, err := catalogChecksum(EmbeddedCatalog)
	require.NoError(t, err)

	// A user added a custom entry but left the seed checksum (baseline matches
	// the current binary), so the file is used as-is.
	entries := append(slices.Clone(EmbeddedCatalog), customEntry("my-custom-model"))
	writeManifest(t, path, catalogManifest{
		SchemaVersion:   catalogSchemaVersion,
		CatalogChecksum: checksum,
		Entries:         entries,
	})

	require.NoError(t, LoadCatalog(dir))

	got, found := GetCatalogEntry("my-custom-model")
	require.True(t, found, "custom entry must be resolvable")
	assert.Equal(t, "Custom my-custom-model", got.Name)

	visibleIDs := idSet(VisibleCatalog())
	assert.True(t, visibleIDs["my-custom-model"], "custom entry must appear in the visible catalog")
}

func TestLoadCatalog_MalformedJSONFallsBackAndLeavesFileUntouched(t *testing.T) {
	resetActiveCatalog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, catalogFileName)

	const bad = "{ this is not valid json"
	require.NoError(t, os.WriteFile(path, []byte(bad), 0o600))

	require.NoError(t, LoadCatalog(dir))

	// Falls back to embedded; the custom entry is absent.
	assert.Equal(t, idSet(EmbeddedCatalog), idSet(ActiveCatalog()))

	// The malformed file is left exactly as it was.
	data, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	assert.Equal(t, bad, string(data), "malformed file must not be overwritten")
}

func TestLoadCatalog_ValidationFailureFallsBack(t *testing.T) {
	tests := []struct {
		name    string
		entries []CatalogEntry
	}{
		{
			name:    "empty entries",
			entries: []CatalogEntry{},
		},
		{
			name:    "missing id",
			entries: []CatalogEntry{{Name: "no id", Files: []CatalogFile{{RemotePath: "m", LocalName: "m", Role: RoleModel}}}},
		},
		{
			name:    "duplicate id",
			entries: []CatalogEntry{customEntry("dup"), customEntry("dup")},
		},
		{
			name: "file missing role",
			entries: []CatalogEntry{{
				ID:    "bad-file",
				Files: []CatalogFile{{RemotePath: "m", LocalName: "m"}}, // role empty
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetActiveCatalog(t)

			dir := t.TempDir()
			path := filepath.Join(dir, catalogFileName)
			writeManifest(t, path, catalogManifest{
				SchemaVersion:   catalogSchemaVersion,
				CatalogChecksum: "irrelevant",
				Entries:         tt.entries,
			})
			before, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path
			require.NoError(t, err)

			require.NoError(t, LoadCatalog(dir))

			assert.Equal(t, idSet(EmbeddedCatalog), idSet(ActiveCatalog()), "must fall back to embedded")

			after, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path
			require.NoError(t, err)
			assert.Equal(t, before, after, "invalid file must not be overwritten")
		})
	}
}

func TestLoadCatalog_RefreshesPristineFileOnChangedEmbedded(t *testing.T) {
	resetActiveCatalog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, catalogFileName)

	// Simulate an older release: a pristine file generated from a catalog that
	// differs from the current embedded one (here, embedded minus its last entry).
	require.Greater(t, len(EmbeddedCatalog), 1)
	oldEntries := slices.Clone(EmbeddedCatalog[:len(EmbeddedCatalog)-1])
	oldChecksum, err := catalogChecksum(oldEntries)
	require.NoError(t, err)
	writeManifest(t, path, catalogManifest{
		SchemaVersion:   catalogSchemaVersion,
		CatalogChecksum: oldChecksum, // pristine: matches the (old) entries
		Entries:         oldEntries,
	})

	require.NoError(t, LoadCatalog(dir))

	// The file is refreshed to the current embedded catalog.
	m := readManifest(t, path)
	embeddedChecksum, err := catalogChecksum(EmbeddedCatalog)
	require.NoError(t, err)
	assert.Equal(t, embeddedChecksum, m.CatalogChecksum, "checksum must be updated to the new baseline")
	assert.Equal(t, idSet(EmbeddedCatalog), idSet(m.Entries), "file must be refreshed to the full embedded catalog")
	assert.Equal(t, idSet(EmbeddedCatalog), idSet(ActiveCatalog()))
}

func TestLoadCatalog_PreservesEditedFileOnChangedEmbedded(t *testing.T) {
	resetActiveCatalog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, catalogFileName)

	// An edited file: entries include a custom model, and the stored checksum is
	// an old baseline that matches neither the current embedded catalog nor the
	// file's own entries (so the file is detected as edited, not pristine).
	staleBaseline, err := catalogChecksum([]CatalogEntry{EmbeddedCatalog[0]})
	require.NoError(t, err)
	entries := append(slices.Clone(EmbeddedCatalog), customEntry("user-pinned"))
	writeManifest(t, path, catalogManifest{
		SchemaVersion:   catalogSchemaVersion,
		CatalogChecksum: staleBaseline,
		Entries:         entries,
	})
	before, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)

	require.NoError(t, LoadCatalog(dir))

	// User edits are preserved in memory and the file is left untouched.
	_, found := GetCatalogEntry("user-pinned")
	assert.True(t, found, "user edits must be preserved")

	after, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	assert.Equal(t, before, after, "edited file must not be overwritten")
}

func TestLoadCatalog_SchemaVersionMismatchStillLoads(t *testing.T) {
	resetActiveCatalog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, catalogFileName)

	checksum, err := catalogChecksum(EmbeddedCatalog)
	require.NoError(t, err)
	entries := append(slices.Clone(EmbeddedCatalog), customEntry("future-entry"))
	writeManifest(t, path, catalogManifest{
		SchemaVersion:   catalogSchemaVersion + 999, // unknown/newer format
		CatalogChecksum: checksum,
		Entries:         entries,
	})

	require.NoError(t, LoadCatalog(dir))

	_, found := GetCatalogEntry("future-entry")
	assert.True(t, found, "best-effort load must still apply a valid file with an unknown schema version")
}

func TestLoadCatalog_AtomicWriteLeavesNoTempFile(t *testing.T) {
	resetActiveCatalog(t)

	dir := t.TempDir()
	require.NoError(t, LoadCatalog(dir))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "no temporary file should remain after an atomic write")
	}
	// Exactly the catalog file should exist.
	require.Len(t, entries, 1)
	assert.Equal(t, catalogFileName, entries[0].Name())
}

func TestLoadCatalog_WriteFailureFallsBackToEmbedded(t *testing.T) {
	resetActiveCatalog(t)

	// Use a path whose parent is a regular file, so MkdirAll/CreateTemp fails.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))
	configDir := filepath.Join(notADir, "config") // child of a file: cannot be created

	err := LoadCatalog(configDir)
	require.Error(t, err, "seed write into an invalid directory must surface an error")

	// Despite the write failure, the active catalog is the embedded fallback.
	assert.Equal(t, idSet(EmbeddedCatalog), idSet(ActiveCatalog()))
}

func TestAccessorsReadActiveCatalog(t *testing.T) {
	resetActiveCatalog(t)

	custom := []CatalogEntry{
		customEntry("only-entry"),
		func() CatalogEntry { e := customEntry("hidden-entry"); e.Hidden = true; return e }(),
	}
	setActiveCatalog(custom)

	// ActiveCatalog returns all entries; VisibleCatalog excludes hidden ones.
	assert.Equal(t, map[string]bool{"only-entry": true, "hidden-entry": true}, idSet(ActiveCatalog()))
	assert.Equal(t, map[string]bool{"only-entry": true}, idSet(VisibleCatalog()))

	_, found := GetCatalogEntry("only-entry")
	assert.True(t, found)
	_, missing := GetCatalogEntry("birdnet-v3.0")
	assert.False(t, missing, "embedded entries must not leak when activeCatalog is set")
}

func TestCatalogChecksum_DeterministicAndRoundTrips(t *testing.T) {
	t.Parallel()

	a, err := catalogChecksum(EmbeddedCatalog)
	require.NoError(t, err)
	b, err := catalogChecksum(EmbeddedCatalog)
	require.NoError(t, err)
	assert.Equal(t, a, b, "checksum must be deterministic")

	// Marshal -> unmarshal -> checksum must equal the original checksum.
	data, err := json.Marshal(EmbeddedCatalog)
	require.NoError(t, err)
	var roundTripped []CatalogEntry
	require.NoError(t, json.Unmarshal(data, &roundTripped))
	c, err := catalogChecksum(roundTripped)
	require.NoError(t, err)
	assert.Equal(t, a, c, "checksum must survive a JSON round-trip")

	// Order sensitivity: reordering entries must change the checksum. This is the
	// property that lets pristine-vs-edited detection notice content changes.
	require.GreaterOrEqual(t, len(EmbeddedCatalog), 2)
	reordered := slices.Clone(EmbeddedCatalog)
	reordered[0], reordered[1] = reordered[1], reordered[0]
	d, err := catalogChecksum(reordered)
	require.NoError(t, err)
	assert.NotEqual(t, a, d, "reordering entries must change the checksum")
}
