package classifier

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// rollbackFakeClassifier is a no-op inference.Classifier that records whether it
// was Closed, so a reload rollback test can prove the previously-serving backend
// was preserved (never Closed) rather than torn down.
type rollbackFakeClassifier struct {
	closed bool
}

func (f *rollbackFakeClassifier) Predict(_ []float32) ([]float32, error) { return nil, nil }
func (f *rollbackFakeClassifier) NumSpecies() int                        { return 1 }
func (f *rollbackFakeClassifier) Close()                                 { f.closed = true }

var _ inference.Classifier = (*rollbackFakeClassifier)(nil)

// TestReloadModelInternal_RollbackRestoresPreviousModel covers the transactional
// rollback in reloadModelInternal: when a reload fails partway, the previously
// serving model must be fully restored and still serving. This path is otherwise
// 0% unit-covered (NewOrchestrator skips without a real model, and the primary-swap
// tests exercise the orchestrator==nil path). No native model is needed:
//
//   - The two early-failure subtests inject a failure at the FIRST fallible step (a
//     nonexistent TaxonomyPath makes LoadTaxonomyData fail), which runs before a new
//     backend is installed. They exercise the rollback of the state mutated up front,
//     each assertion genuinely contingent on rollback: ModelInfo (reassigned by the
//     switch), TaxonomyMap (set to nil by the failed multi-return load), and Settings
//     (swapped to a clone at entry). Both entry points are covered: the variant-swap
//     path (allowPathChange=true) and the settings-reload path (allowPathChange=false).
//   - The post-init subtest uses the reloadInitFn seam to install a NEW backend and
//     republish the runtime triplet + modelVersion (as the real initializeModel does),
//     then fail. This is the only way to reach the rollback branch that tears down the
//     failed backend, restores the previously-serving one, and reverts the runtime
//     triplet / modelVersion, so those restorations are asserted where they are
//     genuinely contingent rather than vacuous.
func TestReloadModelInternal_RollbackRestoresPreviousModel(t *testing.T) {
	const (
		staleName    = "STALE-PREVIOUS-MODEL"
		staleVersion = "OLD-VERSION-STRING"
		devDevice    = "CPU"
		devBackend   = "tflite"
		devPrecision = "FP16"
	)

	// newServingBirdNET builds a struct-literal BirdNET that models a live,
	// previously-serving primary: a fake classifier, a distinguishable ModelInfo,
	// a published identity + runtime triplet, and a sentinel taxonomy map. The
	// reload it drives always fails at the taxonomy step (badTaxonomyPath), so the
	// caller can assert every snapshot field was rolled back.
	newServingBirdNET := func(t *testing.T, oldInfo ModelInfo) (*BirdNET, *rollbackFakeClassifier, *conf.Settings, TaxonomyMap) {
		t.Helper()
		fake := &rollbackFakeClassifier{}
		// A distinct pointer from both the global settings and the reload's internal
		// clone, so restoration can be asserted by pointer identity.
		oldSettings := conf.CloneSettings(conftest.GetTestSettings())
		oldTax := TaxonomyMap{"Turdus merula": "turmer"}
		badTaxonomyPath := filepath.Join(t.TempDir(), "does-not-exist-taxonomy.json")

		bn := &BirdNET{
			classifier:      fake,
			rangeFilter:     nil,
			Settings:        oldSettings,
			ModelInfo:       oldInfo,
			TaxonomyMap:     oldTax,
			ScientificIndex: ScientificNameIndex{"turmer": "Turdus merula"},
			TaxonomyPath:    badTaxonomyPath,
			modelVersion:    staleVersion,
			speciesCache:    make(map[string]*speciesCacheEntry),
		}
		bn.settingsAtomic.Store(oldSettings)
		bn.setRuntimeInfo(devDevice, devBackend, devPrecision)
		bn.publishIdentity()
		return bn, fake, oldSettings, oldTax
	}

	// assertEarlyFailureRolledBack checks the two early-failure subtests. It asserts
	// the three fields mutated BEFORE the taxonomy step, each genuinely contingent on
	// rollback (delete rollback() and each flips), plus the serving-backend guard.
	assertEarlyFailureRolledBack := func(t *testing.T, bn *BirdNET, fake *rollbackFakeClassifier,
		oldSettings *conf.Settings, oldTax TaxonomyMap, oldInfo ModelInfo,
	) {
		t.Helper()
		// Contingent on rollback: ModelInfo (reassigned by the switch), TaxonomyMap
		// (set to nil by the failed multi-return load), and Settings (swapped to a
		// clone at entry) are all mutated before the taxonomy failure.
		assert.Equal(t, oldInfo, bn.ModelInfo, "ModelInfo must be restored to the previous model")
		assert.Equal(t, oldTax, bn.TaxonomyMap, "taxonomy map must be restored (not the nil returned by the failed load)")
		assert.Same(t, oldSettings, bn.Settings, "settings pointer must be restored")

		// Serving-backend guard: on an early failure the classifier is never swapped,
		// so it must remain the original and stay unclosed. This asserts rollback's
		// classifier.Close "!= oldClassifier" guard did not tear down the live backend
		// (dropping that guard would Close the serving instance and flip fake.closed).
		assert.Same(t, fake, bn.classifier, "classifier must be the previously-serving instance")
		assert.False(t, fake.closed, "previously-serving classifier must not be Closed by a failed reload")
	}

	t.Run("variant-swap path (allowPathChange=true)", func(t *testing.T) {
		// Empty Version and ModelPath drive the cleared-path variant-swap branch,
		// which re-resolves the stock embedded identity: ModelInfo IS reassigned
		// before the taxonomy step, so its restoration is meaningfully exercised.
		settings := conftest.GetTestSettings()
		settings.BirdNET.Version = ""
		settings.BirdNET.ModelPath = ""
		conftest.SetTestSettings(settings)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })

		oldInfo := ModelInfo{ID: "TEST_PREV_MODEL", Name: staleName}
		bn, fake, oldSettings, oldTax := newServingBirdNET(t, oldInfo)

		err := bn.reloadModelInternal(true)

		require.Error(t, err, "reload must fail when the taxonomy file cannot be read")
		assert.Contains(t, err.Error(), "taxonomy", "error must identify the failed taxonomy step")
		assertEarlyFailureRolledBack(t, bn, fake, oldSettings, oldTax, oldInfo)
	})

	t.Run("settings-reload path (allowPathChange=false)", func(t *testing.T) {
		// A configured ModelPath drives the birdnet-slot branch. The previous
		// model's CustomPath matches the configured path, so the reload is accepted
		// in place (not refused as a restart-required change) and ModelInfo is
		// reassigned from the path before the taxonomy step fails.
		modelPath := filepath.Join(t.TempDir(), "birdnet-v2.4.tflite")
		settings := conftest.GetTestSettings()
		settings.BirdNET.Version = ""
		settings.BirdNET.ModelPath = modelPath
		conftest.SetTestSettings(settings)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })

		oldInfo := ModelInfo{ID: "TEST_PREV_MODEL", Name: staleName, CustomPath: modelPath}
		bn, fake, oldSettings, oldTax := newServingBirdNET(t, oldInfo)

		err := bn.reloadModelInternal(false)

		require.Error(t, err, "reload must fail when the taxonomy file cannot be read")
		assert.Contains(t, err.Error(), "taxonomy", "error must identify the failed taxonomy step")
		assertEarlyFailureRolledBack(t, bn, fake, oldSettings, oldTax, oldInfo)
	})

	// The strongest case: a failure AFTER a new backend has been installed. The
	// reloadInitFn seam installs a fresh classifier and republishes the runtime
	// triplet + modelVersion (as the real initializeModel does), then returns an
	// error. This exercises the rollback branch that tears down the failed new
	// backend, restores the previously-serving one, and reverts the runtime triplet
	// and version string, none of which the early-failure subtests reach.
	t.Run("post-init failure tears down new backend and restores previous", func(t *testing.T) {
		settings := conftest.GetTestSettings()
		settings.BirdNET.Version = ""
		settings.BirdNET.ModelPath = ""
		conftest.SetTestSettings(settings)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })

		oldInfo := ModelInfo{ID: "TEST_PREV_MODEL", Name: staleName}
		bn, oldFake, oldSettings, _ := newServingBirdNET(t, oldInfo)
		// Embedded taxonomy + labels so the reload proceeds past those steps to the
		// model-init seam (the variant-swap branch resolves the stock identity, whose
		// embedded labels load without a native model).
		bn.TaxonomyPath = ""

		newFake := &rollbackFakeClassifier{}
		bn.reloadInitFn = func() error {
			bn.classifier = newFake
			bn.setRuntimeInfo("GPU", "onnx", "FP32")
			bn.modelVersion = "NEW-VERSION-STRING"
			return fmt.Errorf("simulated post-init failure")
		}

		err := bn.reloadModelInternal(true)

		require.Error(t, err, "reload must surface the post-init failure")
		// The failed reload's new backend must be torn down; the previous one restored
		// and still serving. Both flip if rollback's classifier handling regresses.
		assert.True(t, newFake.closed, "the failed reload's new backend must be Closed")
		assert.Same(t, oldFake, bn.classifier, "the previously-serving classifier must be restored")
		assert.False(t, oldFake.closed, "the previously-serving classifier must not be Closed")
		// The runtime triplet and version string must describe the restored model, not
		// the failed attempt (these are the assertions the early-failure subtests could
		// not exercise, because the triplet/version are only mutated here).
		assert.Equal(t, staleVersion, bn.modelVersion, "model version must be restored")
		device, backend, precision := bn.RuntimeInfo()
		assert.Equal(t, devDevice, device, "runtime device must be restored")
		assert.Equal(t, devBackend, backend, "runtime backend must be restored")
		assert.Equal(t, devPrecision, precision, "runtime precision must be restored")
		// ModelInfo and settings restored, and the identity getters revert too.
		assert.Equal(t, oldInfo, bn.ModelInfo, "ModelInfo must be restored")
		assert.Same(t, oldSettings, bn.Settings, "settings pointer must be restored")
		assert.Equal(t, staleName, bn.ModelName(), "ModelName getter must reflect the restored model")
		assert.Equal(t, staleVersion, bn.ModelVersion(), "ModelVersion getter must reflect the restored model")
	})
}
