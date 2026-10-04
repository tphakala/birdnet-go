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
		{
			name:    "entry with no files",
			entries: []CatalogEntry{{ID: "no-files"}}, // empty Files
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

func TestValidateCatalog_Variants(t *testing.T) {
	t.Parallel()

	validFile := CatalogFile{RemotePath: "m.onnx", LocalName: "m.onnx", Role: RoleModel}
	tests := []struct {
		name    string
		entry   CatalogEntry
		wantErr bool
	}{
		{
			name:  "variant entry with empty top-level Files is valid",
			entry: CatalogEntry{ID: "v", Variants: []CatalogVariant{{ID: "fp32", Files: []CatalogFile{validFile}}}},
		},
		{
			name:    "neither files nor variants",
			entry:   CatalogEntry{ID: "empty"},
			wantErr: true,
		},
		{
			name:    "variant declares no files",
			entry:   CatalogEntry{ID: "v", Variants: []CatalogVariant{{ID: "fp32"}}},
			wantErr: true,
		},
		{
			name:    "variant has empty id",
			entry:   CatalogEntry{ID: "v", Variants: []CatalogVariant{{Files: []CatalogFile{validFile}}}},
			wantErr: true,
		},
		{
			name: "duplicate variant id",
			entry: CatalogEntry{ID: "v", Variants: []CatalogVariant{
				{ID: "x", Files: []CatalogFile{validFile}},
				{ID: "x", Files: []CatalogFile{validFile}},
			}},
			wantErr: true,
		},
		{
			name:    "variant file missing role",
			entry:   CatalogEntry{ID: "v", Variants: []CatalogVariant{{ID: "fp32", Files: []CatalogFile{{RemotePath: "m", LocalName: "m"}}}}},
			wantErr: true,
		},
		{
			name:    "variant without a model-role file",
			entry:   CatalogEntry{ID: "v", Variants: []CatalogVariant{{ID: "fp32", Files: []CatalogFile{{RemotePath: "l.txt", LocalName: "l.txt", Role: RoleLabels}}}}},
			wantErr: true,
		},
		{
			name: "both top-level files and variants",
			entry: CatalogEntry{
				ID:       "both",
				Files:    []CatalogFile{validFile},
				Variants: []CatalogVariant{{ID: "fp32", Files: []CatalogFile{validFile}}},
			},
			wantErr: true,
		},
		{
			name: "built-in baseline with no files is valid",
			entry: CatalogEntry{ID: "v", Variants: []CatalogVariant{
				{ID: "builtin", BuiltIn: true, Default: true},
				{ID: "fp32", Files: []CatalogFile{validFile}},
			}},
		},
		{
			name: "built-in baseline may be the only variant",
			entry: CatalogEntry{ID: "v", Variants: []CatalogVariant{
				{ID: "builtin", BuiltIn: true, Default: true},
			}},
		},
		{
			name: "built-in variant carrying files is rejected",
			entry: CatalogEntry{ID: "v", Variants: []CatalogVariant{
				{ID: "builtin", BuiltIn: true, Files: []CatalogFile{validFile}},
			}},
			wantErr: true,
		},
		{
			name: "more than one built-in variant is rejected",
			entry: CatalogEntry{ID: "v", Variants: []CatalogVariant{
				{ID: "builtin", BuiltIn: true, Default: true},
				{ID: "builtin2", BuiltIn: true},
			}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateCatalog([]CatalogEntry{tt.entry})
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadCatalog_ResolvesVariantEntryFromFile(t *testing.T) {
	resetActiveCatalog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, catalogFileName)

	checksum, err := catalogChecksum(EmbeddedCatalog)
	require.NoError(t, err)

	// A user added a variant entry but left the seed checksum (baseline matches the
	// current binary), so the file loads as-is. Its Files must resolve at runtime.
	variantEntry := CatalogEntry{
		ID: "variant-model", Name: "Variant Model", Category: CategoryBird, Version: "1",
		Variants: []CatalogVariant{
			{ID: "int8", Files: []CatalogFile{{RemotePath: "m_int8.onnx", LocalName: "m_int8.onnx", Role: RoleModel}}},
			{ID: "fp32", Default: true, Files: []CatalogFile{{RemotePath: "m_fp32.onnx", LocalName: "m_fp32.onnx", Role: RoleModel}}},
		},
	}
	entries := append(slices.Clone(EmbeddedCatalog), variantEntry)
	writeManifest(t, path, catalogManifest{
		SchemaVersion:   catalogSchemaVersion,
		CatalogChecksum: checksum,
		Entries:         entries,
	})

	require.NoError(t, LoadCatalog(dir))

	got, found := GetCatalogEntry("variant-model")
	require.True(t, found, "variant entry must load and validate")
	require.Len(t, got.Files, 1)
	assert.Equal(t, "m_fp32.onnx", got.Files[0].LocalName, "Files must resolve to the default variant")
	assert.Len(t, got.Variants, 2, "Variants preserved for the gallery and install path")
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

// depValidationCatalog returns a small valid catalog with a dependency edge, which
// each case below damages in one way.
func depValidationCatalog() []CatalogEntry {
	shared := func(role, name string) []CatalogFile {
		return []CatalogFile{{RemotePath: name, LocalName: name, Role: role}}
	}
	return []CatalogEntry{
		{ID: "top", DependsOn: []string{"geo", "tax"}, Files: []CatalogFile{{RemotePath: "m.onnx", LocalName: "m.onnx", Role: RoleModel}}},
		{ID: "geo", Files: shared(RoleGeomodelModel, "geo.onnx")},
		{ID: "tax", Hidden: true, Component: true, Files: shared(RoleTaxonomy, "tax.csv")},
	}
}

func TestValidateCatalog_Dependencies(t *testing.T) {
	t.Parallel()

	modelFile := []CatalogFile{{RemotePath: "m.onnx", LocalName: "m.onnx", Role: RoleModel}}
	tests := []struct {
		name   string
		mutate func(c []CatalogEntry) []CatalogEntry
		want   string // substring of the error; empty means valid
	}{
		{name: "valid catalog", mutate: func(c []CatalogEntry) []CatalogEntry { return c }},
		{name: "unknown dependency", want: "unknown", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[0].DependsOn = []string{"missing"}
			return c
		}},
		{name: "self dependency", want: "itself", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[0].DependsOn = []string{"top"}
			return c
		}},
		{name: "duplicate dependency", want: "more than once", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[0].DependsOn = []string{"geo", "geo"}
			return c
		}},
		{name: "target with variants", want: "variants", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[1].Files = nil
			c[1].Variants = []CatalogVariant{{ID: "fp32", Files: modelFile}}
			return c
		}},
		{name: "target not shared-only", want: "shared-only", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[1].Files = modelFile
			return c
		}},
		{name: "target with its own dependencies", want: "own dependencies", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[1].DependsOn = []string{"tax"}
			return c
		}},
		{name: "component not hidden", want: "hidden", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[2].Hidden = false
			return c
		}},
		{name: "component not shared-only", want: "shared-only", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[2].Files = modelFile
			return c
		}},
		{name: "component with a registry id", want: "registry", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[2].RegistryID = RegistryIDPerchV2
			return c
		}},
		{name: "component with dependencies", want: "dependencies", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[2].DependsOn = []string{"geo"}
			return c
		}},
		{name: "component with a geomodel file", want: "geomodel", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[2].Files = []CatalogFile{{RemotePath: "g.onnx", LocalName: "g.onnx", Role: RoleGeomodelModel}}
			return c
		}},
		{name: "inline shared file equal to the dependency file", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[1].Files[0].SHA256 = "abc123"
			c[0].Files = append(c[0].Files, c[1].Files[0])
			return c
		}},
		{name: "inline shared file equal by name but without a checksum", want: "differs", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[0].Files = append(c[0].Files, c[1].Files[0])
			return c
		}},
		{name: "inline shared file differing from the dependency file", want: "differs", mutate: func(c []CatalogEntry) []CatalogEntry {
			other := c[1].Files[0]
			other.SHA256 = "different"
			c[0].Files = append(c[0].Files, other)
			return c
		}},
		{name: "own non-shared file named like a dependency file", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[0].Files = append(c[0].Files, CatalogFile{RemotePath: "x", LocalName: "tax.csv", Role: RoleLabels})
			return c
		}},
		{name: "two dependencies installing different files under one name", want: "different contents", mutate: func(c []CatalogEntry) []CatalogEntry {
			c[2].Files = []CatalogFile{{RemotePath: "t", LocalName: "geo.onnx", Role: RoleTaxonomy}}
			return c
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateCatalog(tc.mutate(depValidationCatalog()))
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateCatalog_SyntheticDependencyCatalogAndEmbeddedPass(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateCatalog(dependencyTestCatalog()))
	require.NoError(t, validateCatalog(EmbeddedCatalog))
}

func TestCatalogChecksum_OmitemptyKeepsOldFilesPristine(t *testing.T) {
	t.Parallel()

	// An entry that leaves the new fields zero must marshal without them, so a
	// pristine catalog written by an older version still hashes the same.
	plain, err := json.Marshal(EmbeddedCatalog)
	require.NoError(t, err)
	assert.NotContains(t, string(plain), `"depends_on"`)
	assert.NotContains(t, string(plain), `"component"`)

	var round []CatalogEntry
	require.NoError(t, json.Unmarshal(plain, &round))
	again, err := json.Marshal(round)
	require.NoError(t, err)
	assert.Equal(t, string(plain), string(again), "re-marshalled bytes must be identical")

	want, err := catalogChecksum(EmbeddedCatalog)
	require.NoError(t, err)
	got, err := catalogChecksum(round)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestCatalogChecksum_RoundTripKeepsDependencyFields(t *testing.T) {
	t.Parallel()

	catalog := depValidationCatalog()
	before, err := catalogChecksum(catalog)
	require.NoError(t, err)

	data, err := json.Marshal(catalog)
	require.NoError(t, err)
	var round []CatalogEntry
	require.NoError(t, json.Unmarshal(data, &round))

	assert.Equal(t, []string{"geo", "tax"}, round[0].DependsOn)
	assert.True(t, round[2].Component)
	after, err := catalogChecksum(round)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}
