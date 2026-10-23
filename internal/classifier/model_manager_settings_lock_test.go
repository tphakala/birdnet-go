package classifier

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// heldWriterLatitude is the value the writer that holds the settings lock
// publishes in TestSettingsWriters_ShareTheConfWriterLock.
const heldWriterLatitude = 33.0

// TestSettingsWriters_ShareTheConfWriterLock pins that the model manager's
// config writers and the orchestrator's path correction take the same writer
// lock as every other settings writer (the settings API, the range filter), not
// a private one. While another writer holds conf's lock none of them may
// complete, and once it publishes, each writer's own change lands on top of the
// holder's change instead of replacing it. A writer on a package-private mutex
// would finish while the lock is held and would then publish a clone of the old
// baseline, dropping the holder's latitude.
func TestSettingsWriters_ShareTheConfWriterLock(t *testing.T) {
	// Not parallel: mutates global settings and conf.ConfigPath.
	const (
		batModelPath    = "/models/battybirdnet-eu/model.onnx"
		perchNewPath    = "/models/perch-v2/new.onnx"
		variantNewPath  = "/models/birdnet-v2.4/x.onnx"
		batCatalogEntry = "battybirdnet-eu"
	)

	tests := []struct {
		name  string
		seed  func(t *testing.T, s *conf.Settings, modelsDir string)
		write func(t *testing.T, mm *ModelManager)
		check func(t *testing.T, final *conf.Settings)
	}{
		{
			name: "applyConfigForInstall",
			write: func(t *testing.T, mm *ModelManager) {
				t.Helper()
				entry, ok := GetCatalogEntry(batCatalogEntry)
				require.True(t, ok)
				mm.applyConfigForInstall(&entry, batModelPath, "", "")
			},
			check: func(t *testing.T, final *conf.Settings) {
				t.Helper()
				assert.Equal(t, batModelPath, final.Bat.ClassifierModel)
				assert.Contains(t, final.Models.Enabled, conf.ModelIDBat)
			},
		},
		{
			name: "applyConfigForUninstall with mm.mu held",
			seed: func(_ *testing.T, s *conf.Settings, _ string) {
				s.Bat.ClassifierModel = batModelPath
				s.Models.Enabled = []string{conf.ModelIDBirdNET, conf.ModelIDBat}
			},
			write: func(t *testing.T, mm *ModelManager) {
				t.Helper()
				entry, ok := GetCatalogEntry(batCatalogEntry)
				require.True(t, ok)
				// The production caller holds mm.mu for writing; that must be a
				// permitted order relative to the writer lock.
				mm.mu.Lock()
				defer mm.mu.Unlock()
				mm.applyConfigForUninstall(&entry)
			},
			check: func(t *testing.T, final *conf.Settings) {
				t.Helper()
				assert.Empty(t, final.Bat.ClassifierModel)
				assert.NotContains(t, final.Models.Enabled, conf.ModelIDBat)
			},
		},
		{
			name: "applyConfigForVariantSwap",
			write: func(_ *testing.T, mm *ModelManager) {
				mm.applyConfigForVariantSwap(RegistryIDBirdNETV24, variantNewPath, nil)
			},
			check: func(t *testing.T, final *conf.Settings) {
				t.Helper()
				assert.Equal(t, variantNewPath, final.BirdNET.ModelPath)
			},
		},
		{
			name: "captureLegacyAutoEnableOnce",
			write: func(_ *testing.T, mm *ModelManager) {
				mm.captureLegacyAutoEnableOnce(GetLogger(), nil)
			},
			check: func(t *testing.T, final *conf.Settings) {
				t.Helper()
				assert.True(t, final.Models.AutoEnableMigrated)
			},
		},
		{
			name: "applyPathCorrection",
			seed: func(t *testing.T, s *conf.Settings, modelsDir string) {
				t.Helper()
				entry, ok := GetCatalogEntry("perch-v2")
				require.True(t, ok)
				galleryName := modelRoleLocalName(t, variantByID(t, &entry, "fp32").Files)
				// A stale path under the gallery layout is one the orchestrator may repair.
				s.Perch.ModelPath = filepath.Join(modelsDir, "perch-v2", galleryName)
			},
			write: func(_ *testing.T, mm *ModelManager) {
				o := &Orchestrator{}
				o.SetModelsDir(mm.modelsDir)
				o.applyPathCorrection(&pendingPathCorrection{
					registryID: RegistryIDPerchV2,
					resolved:   modelFileSet{model: perchNewPath},
					repairable: true,
				})
			},
			check: func(t *testing.T, final *conf.Settings) {
				t.Helper()
				assert.Equal(t, perchNewPath, final.Perch.ModelPath)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateTestConfig(t)
			origSettings := conf.GetSettings()
			t.Cleanup(func() { conf.StoreSettings(origSettings) })

			modelsDir := t.TempDir()
			settings := conftest.GetTestSettings()
			if tt.seed != nil {
				tt.seed(t, settings, modelsDir)
			}
			conf.StoreSettings(settings)
			mm := NewModelManager(modelsDir, nil, settings)

			release := conftest.HoldSettingsWriter(t, func(updated *conf.Settings) {
				updated.BirdNET.Latitude = heldWriterLatitude
			})

			done := make(chan struct{})
			go func() {
				defer close(done)
				tt.write(t, mm)
			}()

			conftest.AssertStillBlocked(t, done, tt.name)
			release()
			<-done

			final := conf.GetSettings()
			assert.InDelta(t, heldWriterLatitude, final.BirdNET.Latitude, 0,
				"the writer that held the lock must not lose its change to a writer built on the old baseline")
			tt.check(t, final)
		})
	}
}
