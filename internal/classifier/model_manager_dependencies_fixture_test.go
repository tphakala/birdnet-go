package classifier

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// Synthetic catalog IDs for the dependency tests. A and B are flat classifiers that
// depend on the geomodel G and the hidden taxonomy component T; P is a permanent
// (v2.4-style) entry whose downloaded variant depends on G.
const (
	depIDA = "dep-a"
	depIDB = "dep-b"
	depIDP = "dep-primary"
	depIDG = "dep-geo"
	depIDT = "dep-tax"

	depVariantBuiltin = "builtin"
	depVariantDFT     = "fp32-dfttrunc"
	depGeomodelVer    = "v3"

	depLocalGeoModel  = "geo.onnx"
	depLocalGeoLabels = "geo_labels.txt"
	depLocalTaxonomy  = "taxonomy.csv"
	depLocalDFT       = "p-dft.onnx"
)

// depServer serves a deterministic payload for every requested path, counts the
// requests per path, and can be told to answer 404 for a path.
type depServer struct {
	*httptest.Server
	mu      sync.Mutex
	hits    map[string]int
	missing map[string]bool
}

// depPayload is the content the test server returns for a remote path.
func depPayload(remote string) []byte { return []byte("payload:" + remote) }

func newDepServer(t *testing.T) *depServer {
	t.Helper()
	d := &depServer{hits: map[string]int{}, missing: map[string]bool{}}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remote := r.URL.Path[1:]
		d.mu.Lock()
		d.hits[remote]++
		gone := d.missing[remote]
		d.mu.Unlock()
		if gone {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(depPayload(remote))
	}))
	t.Cleanup(d.Close)
	return d
}

// Hits returns how many times remote was requested.
func (d *depServer) Hits(remote string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hits[remote]
}

// Fail makes remote answer 404 from now on.
func (d *depServer) Fail(remote string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.missing[remote] = true
}

// depFile builds a catalog file whose checksum and size match depPayload(remote).
func depFile(role, remote, local string) CatalogFile {
	p := depPayload(remote)
	return CatalogFile{RemotePath: remote, LocalName: local, Role: role, SHA256: sha256Hex(p), SizeBytes: int64(len(p))}
}

// depClassifier builds a flat classifier entry that depends on G and T.
func depClassifier(id, registryID string) CatalogEntry {
	return CatalogEntry{
		ID:              id,
		Name:            "Test " + id,
		Version:         "1.0",
		Category:        CategoryBird,
		RegistryID:      registryID,
		HuggingFaceRepo: "t/" + id,
		DependsOn:       []string{depIDG, depIDT},
		Files: []CatalogFile{
			depFile(RoleModel, id+"-model.onnx", id+"-model.onnx"),
			depFile(RoleLabels, id+"-labels.txt", id+"-labels.txt"),
		},
	}
}

// dependencyTestCatalog returns the synthetic catalog: A and B (flat, depend on G
// and T), P (permanent shape: builtin baseline plus a DFT variant, depends on G), G
// (shared-only geomodel) and T (hidden taxonomy component).
func dependencyTestCatalog() []CatalogEntry {
	return []CatalogEntry{
		depClassifier(depIDA, RegistryIDPerchV2),
		depClassifier(depIDB, RegistryIDBirdNETV3),
		{
			ID:              depIDP,
			Name:            "Test primary",
			Version:         "2.4",
			Category:        CategoryBird,
			RegistryID:      RegistryIDBirdNETV24,
			HuggingFaceRepo: "t/primary",
			DependsOn:       []string{depIDG},
			Variants: []CatalogVariant{
				{ID: depVariantBuiltin, BuiltIn: true, Default: true},
				{ID: depVariantDFT, Precision: "fp32", Files: []CatalogFile{depFile(RoleModel, "p-dft.onnx", depLocalDFT)}},
			},
		},
		{
			ID:              depIDG,
			Name:            "Test geomodel",
			Version:         "3.0",
			Category:        CategoryGeomodel,
			GeomodelVersion: depGeomodelVer,
			HuggingFaceRepo: "t/geo",
			Files: []CatalogFile{
				depFile(RoleGeomodelModel, "g-model.onnx", depLocalGeoModel),
				depFile(RoleGeomodelLabels, "g-labels.txt", depLocalGeoLabels),
			},
		},
		{
			ID:              depIDT,
			Name:            "Test taxonomy",
			Version:         "1.0",
			Category:        CategoryBird,
			Hidden:          true,
			Component:       true,
			HuggingFaceRepo: "t/tax",
			Files:           []CatalogFile{depFile(RoleTaxonomy, "t-taxonomy.csv", depLocalTaxonomy)},
		},
	}
}

// depHarness wires the synthetic catalog, a download server, isolated settings and a
// ModelManager whose range-filter reload is observable. Tests using it are serial: it
// replaces the package-global catalog and settings.
type depHarness struct {
	t         *testing.T
	modelsDir string
	srv       *depServer
	mm        *ModelManager
	reloads   int
	reloadErr error
}

func newDepHarness(t *testing.T) *depHarness {
	t.Helper()
	isolateTestConfig(t)
	orig := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(orig) })
	settings := conftest.GetTestSettings()
	conf.StoreSettings(settings)

	setActiveCatalog(dependencyTestCatalog())
	t.Cleanup(func() { setActiveCatalog(nil) })

	h := &depHarness{t: t, modelsDir: t.TempDir(), srv: newDepServer(t)}
	h.mm = h.newManager()
	return h
}

// newManager builds a manager over the harness models dir with the reload seam wired.
func (h *depHarness) newManager() *ModelManager {
	mm := NewModelManager(h.modelsDir, nil, conf.GetSettings())
	mm.reloadRangeFilterFn = func() error {
		h.reloads++
		return h.reloadErr
	}
	return mm
}

// entry returns the active catalog entry id.
func (h *depHarness) entry(id string) CatalogEntry {
	h.t.Helper()
	e, ok := GetCatalogEntry(id)
	require.True(h.t, ok, "catalog entry %s", id)
	return e
}

// install installs (or, for the permanent entry, switches to) variant of id.
func (h *depHarness) install(id, variant string) error {
	e := h.entry(id)
	if IsPermanentEntry(&e) {
		return h.mm.InstallOrReplace(h.t.Context(), &e, variant, h.srv.URL, nil)
	}
	return h.mm.Install(h.t.Context(), &e, variant, h.srv.URL, nil)
}

// shared returns the path of a file in models/shared.
func (h *depHarness) shared(name string) string {
	return filepath.Join(h.modelsDir, sharedDirName, name)
}

// own returns the path of a file in a model's own directory.
func (h *depHarness) own(id, name string) string { return filepath.Join(h.modelsDir, id, name) }

// writeShared writes the correct payload of remote as shared file name.
func (h *depHarness) writeShared(name, remote string) string {
	h.t.Helper()
	p := h.shared(name)
	require.NoError(h.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(h.t, os.WriteFile(p, depPayload(remote), 0o644))
	return p
}

// setDownloading registers a synthetic in-flight or failed download state.
func (h *depHarness) setDownloading(id, status string) {
	h.mm.mu.Lock()
	h.mm.downloading[id] = &DownloadState{CatalogID: id, Status: status}
	h.mm.mu.Unlock()
}

// installedSnapshot copies mm.installed.
func installedSnapshot(mm *ModelManager) map[string]InstalledModel {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	out := make(map[string]InstalledModel, len(mm.installed))
	maps.Copy(out, mm.installed)
	return out
}

// scanFresh returns what a fresh manager derives from disk, as ScanInstalled would
// at startup (no orchestrator, no settings writes). The permanent entry is always
// reported by a scan, so it is dropped unless the harness manager has a record for it.
func (h *depHarness) scanFresh() map[string]InstalledModel {
	mm := NewModelManager(h.modelsDir, nil, nil)
	mm.ScanInstalled()
	out := installedSnapshot(mm)
	if _, has := installedSnapshot(h.mm)[depIDP]; !has {
		delete(out, depIDP)
	}
	return out
}

// rescan replaces the harness manager with a fresh one that scanned disk, like a restart.
func (h *depHarness) rescan() {
	mm := h.newManager()
	mm.ScanInstalled()
	h.mm = mm
}

// requireSameRecords asserts two installed maps agree on ids, variants, paths and
// versions (InstalledAt excluded: it is a time, not part of the identity).
func requireSameRecords(t *testing.T, want, got map[string]InstalledModel, msg string) {
	t.Helper()
	strip := func(m map[string]InstalledModel) map[string]InstalledModel {
		out := make(map[string]InstalledModel, len(m))
		for k, v := range m {
			v.InstalledAt = time.Time{}
			out[k] = v
		}
		return out
	}
	assert.Equal(t, strip(want), strip(got), msg)
}
