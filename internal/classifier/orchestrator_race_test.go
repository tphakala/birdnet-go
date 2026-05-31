package classifier

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestOrchestrator_ConcurrentReloadAndReads_NoRace is the regression guard for
// the orchestrator ModelInfo reload-vs-read data race. The orchestrator must
// read its own o.mu-guarded o.ModelInfo.ID,
// not the primary BirdNET's bn.mu-guarded ModelInfo.ID, when it iterates the
// models map under o.mu. BirdNET.ReloadModel mutates bn.ModelInfo under bn.mu
// (never under o.mu), so a model reload running concurrently with
// GetAllProbableSpeciesWithSettings / RangeFilterStatus is a data race on
// ModelInfo unless the orchestrator consults its own synced copy.
//
// Must be run with -race. The reloader spins for the full duration of the
// readers so the write window always overlaps the reads.
func TestOrchestrator_ConcurrentReloadAndReads_NoRace(t *testing.T) {
	const primaryID = "BirdNET_V3"

	settings := universalSettings(t)
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true

	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird"},
		scores:    []SpeciesScore{{Score: 0.9, Label: "Turdus merula_Common Blackbird"}},
		rawScores: []float32{0.9},
	}

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		rangeFilter:  rf,
	}
	bn.ModelInfo = ModelInfo{ID: primaryID, Name: "BirdNET v3.0"}

	nonPrimary := &mockModelInstance{id: "Perch_V2", labels: []string{"Aratinga solstitialis"}}

	o := &Orchestrator{
		Settings:  settings,
		ModelInfo: bn.ModelInfo, // o.mu-guarded copy, mirrors the primary (as NewOrchestrator wires it)
		primary:   bn,
		models: map[string]*modelEntry{
			primaryID:  {instance: bn},
			"Perch_V2": {instance: nonPrimary},
		},
	}

	const readsPerGoroutine = 200
	const readerCount = 4

	var readerWg sync.WaitGroup
	var readersDone atomic.Bool
	start := make(chan struct{})

	// Reloader: mutate bn.ModelInfo under bn.mu exactly as BirdNET.ReloadModel
	// does. The model identity never changes across reloads (ReloadModel rejects
	// identity changes), so the ID stays primaryID; only the write matters here.
	// It spins until every reader has finished so the write window always
	// overlaps the reads. Joined via reloaderDone, so no WaitGroup is needed.
	reloaderDone := make(chan struct{})
	go func() {
		defer close(reloaderDone)
		<-start
		for !readersDone.Load() {
			bn.mu.Lock()
			bn.ModelInfo = ModelInfo{ID: primaryID, Name: "BirdNET v3.0"}
			bn.mu.Unlock()
		}
	}()

	// Readers: hammer the two orchestrator methods that read the primary ID
	// while iterating o.models under o.mu.
	for range readerCount {
		readerWg.Go(func() {
			<-start
			for range readsPerGoroutine {
				if _, err := o.GetAllProbableSpeciesWithSettings(time.Now(), 0, settings); err != nil {
					t.Errorf("GetAllProbableSpeciesWithSettings returned error: %v", err)
				}
				_ = o.RangeFilterStatus()
			}
		})
	}

	close(start)
	readerWg.Wait()
	readersDone.Store(true)
	<-reloaderDone
}

// TestOrchestrator_ConcurrentResolverRegistrationAndResolve_NoRace is the
// regression guard for the nameResolvers append/read data race.
// registerTaxonomyResolver appends to
// o.nameResolvers and ResolveName iterates it from the inference path; both must
// be synchronized with o.mu so a dynamic resolver registration cannot corrupt
// the slice header for concurrent readers. The fix is also idempotent: exactly
// one taxonomy resolver is registered even under concurrent registration.
//
// Must be run with -race. Readers spin for the full duration of the writers so
// the append window always overlaps the reads.
func TestOrchestrator_ConcurrentResolverRegistrationAndResolve_NoRace(t *testing.T) {
	// Place a taxonomy fixture at {dir}/shared/taxonomy.csv so
	// registerTaxonomyResolver loads a real resolver and performs the append.
	dir := t.TempDir()
	sharedDir := filepath.Join(dir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, "taxonomy.csv"), []byte(testTaxonomyCSV), 0o644))

	settings := conf.GetTestSettings()
	settings.BirdNET.Locale = "en-uk"

	o := &Orchestrator{
		Settings:      settings,
		nameResolvers: []NameResolver{NewBirdNETLabelResolver([]string{"Struthio camelus_Common Ostrich"})},
	}

	const writerCount = 8
	const readerCount = 4

	var writerWg, readerWg sync.WaitGroup
	var writersDone atomic.Bool
	start := make(chan struct{})

	// Writers: register the taxonomy resolver concurrently. Only the first
	// should append; the guarded double-check keeps it race-free and idempotent.
	for range writerCount {
		writerWg.Go(func() {
			<-start
			o.registerTaxonomyResolver(dir)
		})
	}

	// Readers: resolve names off the inference path while the append happens.
	// They spin until every writer has finished registering.
	for range readerCount {
		readerWg.Go(func() {
			<-start
			for !writersDone.Load() {
				_ = o.ResolveName("Struthio camelus", "")
			}
		})
	}

	close(start)
	writerWg.Wait()
	writersDone.Store(true)
	readerWg.Wait()

	// The double-checked append must register exactly one taxonomy resolver.
	count := 0
	for _, r := range o.nameResolvers {
		if _, ok := r.(*TaxonomyResolver); ok {
			count++
		}
	}
	assert.Equal(t, 1, count, "exactly one taxonomy resolver must be registered under concurrent registration")
}

// TestOrchestrator_ConcurrentReloadSnapshot_NoRace verifies that calling ReloadSnapshot
// on the primary model concurrently with writes to its ModelInfo / Taxonomy fields does not race.
func TestOrchestrator_ConcurrentReloadSnapshot_NoRace(t *testing.T) {
	bn := &BirdNET{}
	bn.ModelInfo = ModelInfo{ID: "BirdNET_V3", Name: "BirdNET v3.0"}
	bn.TaxonomyMap = TaxonomyMap{"test": "test"}
	bn.TaxonomyPath = "path/to/taxonomy"
	bn.ScientificIndex = ScientificNameIndex{"test": "test"}

	start := make(chan struct{})
	const iterations = 500
	var wg sync.WaitGroup

	// Writer goroutine: simulates ReloadModel writing to BirdNET fields under bn.mu
	wg.Go(func() {
		<-start
		for range iterations {
			bn.mu.Lock()
			bn.ModelInfo = ModelInfo{ID: "BirdNET_V3", Name: "BirdNET v3.0"}
			bn.TaxonomyMap = TaxonomyMap{"test": "test"}
			bn.TaxonomyPath = "path/to/taxonomy"
			bn.ScientificIndex = ScientificNameIndex{"test": "test"}
			bn.mu.Unlock()
		}
	})

	// Reader goroutine: simulates Step-2 reading the fields via ReloadSnapshot
	wg.Go(func() {
		<-start
		for range iterations {
			info, taxMap, taxPath, sciIndex := bn.ReloadSnapshot()
			_ = info
			_ = taxMap
			_ = taxPath
			_ = sciIndex
		}
	})

	close(start)
	wg.Wait()
}

// TestBirdNET_ConcurrentSettingsReadsAndWrites_NoRace verifies that calling currentSettings
// and Debug concurrently with simulated ReloadModel settings updates does not cause a data race.
func TestBirdNET_ConcurrentSettingsReadsAndWrites_NoRace(t *testing.T) {
	settings := &conf.Settings{}
	bn, err := NewBirdNET(settings, nil)
	require.NoError(t, err)

	const iterations = 1000
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Writer: simulates ReloadModel() updating Settings copy
	wg.Go(func() {
		<-start
		for i := range iterations {
			settingsCopy := conf.CloneSettings(settings)
			settingsCopy.BirdNET.Debug = i%2 == 0
			bn.mu.Lock()
			bn.updateSettings(settingsCopy)
			bn.mu.Unlock()
		}
	})

	// Reader: concurrently calls currentSettings, Debug, EnrichResultWithTaxonomy,
	// and GetProbableSpecies. GetProbableSpecies is the path that previously read
	// bn.Settings without synchronization; it now reads via the atomic accessor
	// and this exercises that fix under -race.
	wg.Go(func() {
		<-start
		now := time.Now()
		for range iterations {
			_ = bn.currentSettings()
			bn.Debug("test debug message")
			_, _, _ = bn.EnrichResultWithTaxonomy("Turdus merula_Common Blackbird")
			_, _ = bn.GetProbableSpecies(now, 0)
		}
	})

	close(start)
	wg.Wait()
}

// TestOrchestrator_ConcurrentSettingsReadsAndWrites_NoRace verifies that calling CurrentSettings
// concurrently with simulated ReloadModel settings updates on the Orchestrator does not cause a data race.
func TestOrchestrator_ConcurrentSettingsReadsAndWrites_NoRace(t *testing.T) {
	settings := &conf.Settings{}
	o, err := NewOrchestrator(settings)
	require.NoError(t, err)

	const iterations = 1000
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Writer: simulates ReloadModel() updating settings on Orchestrator
	wg.Go(func() {
		<-start
		for i := range iterations {
			settingsCopy := conf.CloneSettings(settings)
			settingsCopy.BirdNET.Debug = i%2 == 0
			o.mu.Lock()
			o.updateSettings(settingsCopy)
			o.mu.Unlock()
		}
	})

	// Reader: concurrently calls CurrentSettings, Labels, NumSpecies, and
	// GetProbableSpecies (which delegates to the primary's settings-reading path).
	wg.Go(func() {
		<-start
		now := time.Now()
		for range iterations {
			_ = o.CurrentSettings()
			_ = o.Labels()
			_ = o.NumSpecies()
			_, _ = o.GetProbableSpecies(now, 0)
		}
	})

	close(start)
	wg.Wait()
}
