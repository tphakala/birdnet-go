package classifier

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localNames lists the LocalName of each file, in order.
func localNames(files []CatalogFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.LocalName)
	}
	return out
}

// These tests replace the package-global catalog, so none of them is parallel.

func TestEffectiveFiles(t *testing.T) {
	setActiveCatalog(dependencyTestCatalog())
	t.Cleanup(func() { setActiveCatalog(nil) })

	a, _ := GetCatalogEntry(depIDA)
	p, _ := GetCatalogEntry(depIDP)
	g, _ := GetCatalogEntry(depIDG)

	t.Run("variant files then dependency files in declared order", func(t *testing.T) {
		files, ok := EffectiveFiles(&a, "")
		require.True(t, ok)
		assert.Equal(t, []string{
			depIDA + "-model.onnx", depIDA + "-labels.txt",
			depLocalGeoModel, depLocalGeoLabels, depLocalTaxonomy,
		}, localNames(files))
	})

	t.Run("dependency files take the dependency repo", func(t *testing.T) {
		files, _ := EffectiveFiles(&a, "")
		byName := map[string]CatalogFile{}
		for _, f := range files {
			byName[f.LocalName] = f
		}
		assert.Equal(t, "t/geo", byName[depLocalGeoModel].HuggingFaceRepo)
		assert.Equal(t, "t/tax", byName[depLocalTaxonomy].HuggingFaceRepo)
		assert.Empty(t, byName[depIDA+"-model.onnx"].HuggingFaceRepo, "own files keep the entry-level repo rule")
	})

	t.Run("an inline copy wins over the dependency file of the same name", func(t *testing.T) {
		inline := a
		inline.Files = slices.Clone(a.Files)
		inline.Files = append(inline.Files, CatalogFile{
			RemotePath: "inline/geo.onnx", LocalName: depLocalGeoModel, Role: RoleGeomodelModel, SizeBytes: 7,
		})
		files, ok := EffectiveFiles(&inline, "")
		require.True(t, ok)
		var geo []CatalogFile
		for _, f := range files {
			if f.LocalName == depLocalGeoModel {
				geo = append(geo, f)
			}
		}
		require.Len(t, geo, 1, "the dependency copy must be skipped")
		assert.Equal(t, "inline/geo.onnx", geo[0].RemotePath)
		assert.Len(t, files, 5)
	})

	t.Run("the built-in variant gets its dependency files like any variant", func(t *testing.T) {
		for _, id := range []string{"", depVariantBuiltin} {
			files, ok := EffectiveFiles(&p, id)
			require.True(t, ok, "variant %q", id)
			assert.Equal(t, []string{depLocalGeoModel, depLocalGeoLabels}, localNames(files), "variant %q", id)
		}
	})

	t.Run("a downloaded variant gets its dependency files", func(t *testing.T) {
		files, ok := EffectiveFiles(&p, depVariantDFT)
		require.True(t, ok)
		assert.Equal(t, []string{depLocalDFT, depLocalGeoModel, depLocalGeoLabels}, localNames(files))
	})

	t.Run("an unknown variant is not ok", func(t *testing.T) {
		files, ok := EffectiveFiles(&p, "nope")
		assert.False(t, ok)
		assert.Empty(t, files)
	})

	t.Run("an entry without dependencies returns its own files", func(t *testing.T) {
		files, ok := EffectiveFiles(&g, "")
		require.True(t, ok)
		assert.Equal(t, localNames(g.Files), localNames(files))
	})

	t.Run("the catalog's own slices are never mutated", func(t *testing.T) {
		// Spare capacity makes an append onto the catalog's slice write into the
		// shared backing array, which the sentinel check below would see.
		backing := make([]CatalogFile, 2, 16)
		copy(backing, a.Files)
		shared := a
		shared.Files = backing
		geoBefore := slices.Clone(g.Files)

		files, _ := EffectiveFiles(&shared, "")
		require.Len(t, files, 5)

		assert.Equal(t, a.Files, shared.Files, "entry Files must be unchanged")
		assert.Equal(t, CatalogFile{}, backing[:3][2], "nothing may be appended onto the catalog backing array")
		assert.Equal(t, geoBefore, g.Files, "the dependency's own files must be unchanged")
		for _, f := range g.Files {
			assert.Empty(t, f.HuggingFaceRepo, "the repo must not be written into a catalog-owned file")
		}
		fresh, _ := GetCatalogEntry(depIDG)
		for _, f := range fresh.Files {
			assert.Empty(t, f.HuggingFaceRepo)
		}
	})
}

func TestProvidesGeomodel(t *testing.T) {
	setActiveCatalog(dependencyTestCatalog())
	t.Cleanup(func() { setActiveCatalog(nil) })

	cases := []struct {
		id, variant string
		want        bool
	}{
		{depIDA, "", true},
		{depIDG, "", true},
		{depIDT, "", false},
		{depIDP, "", true},
		{depIDP, depVariantBuiltin, true},
		{depIDP, depVariantDFT, true},
		{depIDP, "nope", false},
	}
	for _, tc := range cases {
		e, _ := GetCatalogEntry(tc.id)
		assert.Equal(t, tc.want, ProvidesGeomodel(&e, tc.variant), "%s/%s", tc.id, tc.variant)
	}
	assert.False(t, ProvidesGeomodel(nil, ""))
}

func TestGeomodelDependency(t *testing.T) {
	setActiveCatalog(dependencyTestCatalog())
	t.Cleanup(func() { setActiveCatalog(nil) })

	a, _ := GetCatalogEntry(depIDA)
	p, _ := GetCatalogEntry(depIDP)
	tax, _ := GetCatalogEntry(depIDT)

	dep, ok := geomodelDependency(&a)
	require.True(t, ok)
	assert.Equal(t, depIDG, dep.ID)

	dep, ok = geomodelDependency(&p)
	require.True(t, ok)
	assert.Equal(t, depIDG, dep.ID)

	_, ok = geomodelDependency(&tax)
	assert.False(t, ok)
}

func TestDependencyEntries_SkipsUnknownSelfAndDuplicates(t *testing.T) {
	setActiveCatalog(dependencyTestCatalog())
	t.Cleanup(func() { setActiveCatalog(nil) })

	e := CatalogEntry{ID: "x", DependsOn: []string{depIDT, "missing", "x", depIDG, depIDT}}
	deps := dependencyEntries(&e)
	require.Len(t, deps, 2)
	assert.Equal(t, depIDT, deps[0].ID)
	assert.Equal(t, depIDG, deps[1].ID)
}

// TestEffectiveFiles_EmbeddedCatalogEqualsVariantFiles pins that the dependency
// machinery is inert on the shipped catalog: no embedded entry declares a
// dependency, so the effective files are exactly the variant files. This is expected
// to change when the catalog starts declaring dependencies.
func TestEffectiveFiles_EmbeddedCatalogEqualsVariantFiles(t *testing.T) {
	setActiveCatalog(nil)
	for i := range EmbeddedCatalog {
		entry := &EmbeddedCatalog[i]
		assert.Empty(t, entry.DependsOn, "%s must not declare dependencies yet", entry.ID)
		assert.False(t, entry.Component, "%s must not be a component yet", entry.ID)
		ids := []string{""}
		for _, v := range entry.Variants {
			ids = append(ids, v.ID)
		}
		for _, id := range ids {
			want, wantOK := variantFilesByID(entry, id)
			got, gotOK := EffectiveFiles(entry, id)
			assert.Equal(t, wantOK, gotOK, "%s/%s", entry.ID, id)
			assert.Equal(t, want, got, "%s/%s", entry.ID, id)
		}
	}
	for _, e := range ActiveCatalog() {
		assert.Equal(t, HasGeomodelFiles(&e), ProvidesGeomodel(&e, ""), "%s", e.ID)
	}
}
