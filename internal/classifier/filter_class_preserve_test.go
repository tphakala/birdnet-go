package classifier

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/labels/vocalization"
)

// speciesConfidences returns a species->confidence map for a results slice, so
// tests can assert membership without depending on order.
func speciesConfidences(results []datastore.Results) map[string]float32 {
	m := make(map[string]float32, len(results))
	for _, r := range results {
		m[r.Species] = r.Confidence
	}
	return m
}

// TestGetTopKResults_PreservesHumanAndDogPastTopK is the issue #4177 fix: the
// privacy and dog-bark filters run over the truncated results, so a human or dog
// class ranked below the top-K must still survive truncation or the filters never
// see it. Twelve high-confidence birds outrank a faint "Speech" and "Bark"; both
// must be retained past k=10.
func TestGetTopKResults_PreservesHumanAndDogPastTopK(t *testing.T) {
	t.Parallel()

	results := make([]datastore.Results, 0, 14)
	for i := range 12 {
		results = append(results, datastore.Results{
			Species:    "Bird_" + string(rune('A'+i)),
			Confidence: 0.9 - float32(i)*0.01, // 0.90 .. 0.79, all above the faint classes
		})
	}
	results = append(results,
		datastore.Results{Species: "Speech", Confidence: 0.02},
		datastore.Results{Species: "Bark", Confidence: 0.03},
	)

	top := getTopKResults(results, 10)

	got := speciesConfidences(top)
	require.Contains(t, got, "Speech", "faint human class must survive top-K truncation")
	require.Contains(t, got, "Bark", "faint dog class must survive top-K truncation")
	assert.InDelta(t, 0.02, got["Speech"], 1e-6, "preserved human confidence must be intact")
	assert.InDelta(t, 0.03, got["Bark"], 1e-6, "preserved dog confidence must be intact")
	// 10 birds + 1 human + 1 dog. The two lowest birds (rank 11, 12) are dropped.
	assert.Len(t, top, 12)
}

// TestGetTopKResults_PrivacyExcludedLabelsNotPreserved pins that human-category
// classes the privacy filter ignores ("Hands", "Cough") are not force-retained past
// the top-K, and do not displace a fainter "Speech" as the preserved human class
// even when they score higher. Without the exclusion the strongest human-category
// label, "Hands", would be appended instead of "Speech".
func TestGetTopKResults_PrivacyExcludedLabelsNotPreserved(t *testing.T) {
	t.Parallel()

	results := make([]datastore.Results, 0, 15)
	for i := range 12 {
		results = append(results, datastore.Results{
			Species:    "Bird_" + string(rune('A'+i)),
			Confidence: 0.9 - float32(i)*0.01, // 0.90 .. 0.79, all above the faint classes
		})
	}
	results = append(results,
		datastore.Results{Species: "Hands", Confidence: 0.30},
		datastore.Results{Species: "Cough", Confidence: 0.20},
		datastore.Results{Species: "Speech", Confidence: 0.02},
	)

	top := getTopKResults(results, 10)

	got := speciesConfidences(top)
	assert.NotContains(t, got, "Hands", "privacy-excluded label must not be force-retained")
	assert.NotContains(t, got, "Cough", "privacy-excluded label must not be force-retained")
	require.Contains(t, got, "Speech", "the voiced human class must be the preserved one")
	assert.InDelta(t, 0.02, got["Speech"], 1e-6)
	// 10 birds + the preserved Speech only.
	assert.Len(t, top, 11)
}

// TestGetTopKResults_NoDuplicateWhenClassInTopK verifies preservation does not
// duplicate a human/dog class that already made the top-K.
func TestGetTopKResults_NoDuplicateWhenClassInTopK(t *testing.T) {
	t.Parallel()

	results := []datastore.Results{
		{Species: "Speech", Confidence: 0.95},
		{Species: "Bird_A", Confidence: 0.90},
		{Species: "Bird_B", Confidence: 0.80},
	}

	top := getTopKResults(results, 10)

	var speechCount int
	for _, r := range top {
		if r.Species == "Speech" {
			speechCount++
		}
	}
	assert.Equal(t, 1, speechCount, "human class already in top-K must not be duplicated")
	assert.Len(t, top, 3)
}

// TestGetTopKResults_HumanAndDogPreservedIndependently verifies the human and dog
// classes are retained independently: a human class that makes the top-K must not
// suppress preservation of a dog class that ranks below it, and vice versa.
func TestGetTopKResults_HumanAndDogPreservedIndependently(t *testing.T) {
	t.Parallel()

	results := make([]datastore.Results, 0, 12)
	// A human class is strong enough to make the top-K.
	results = append(results, datastore.Results{Species: "Speech", Confidence: 0.95})
	for i := range 10 {
		results = append(results, datastore.Results{
			Species:    "Bird_" + string(rune('A'+i)),
			Confidence: 0.9 - float32(i)*0.01,
		})
	}
	// A faint dog class ranks below k (11 louder classes already precede it).
	results = append(results, datastore.Results{Species: "Bark", Confidence: 0.02})

	top := getTopKResults(results, 10)
	got := speciesConfidences(top)
	require.Contains(t, got, "Speech", "human class in top-K must remain")
	require.Contains(t, got, "Bark", "faint dog class must be preserved independently of the human class")
}

// TestGetTopKResults_NoFilterClassesUnchanged verifies a normal bird-only chunk
// is unaffected: exactly the top-K by confidence, no growth.
func TestGetTopKResults_NoFilterClassesUnchanged(t *testing.T) {
	t.Parallel()

	results := make([]datastore.Results, 0, 15)
	for i := range 15 {
		results = append(results, datastore.Results{
			Species:    "Bird_" + string(rune('A'+i)),
			Confidence: 0.9 - float32(i)*0.01,
		})
	}

	top := getTopKResults(results, 10)
	assert.Len(t, top, 10, "bird-only chunk must be exactly top-K")
}

// preserveFilterClassesUncached is the pre-cache implementation of
// preserveFilterClasses, kept as the reference the cached version must match.
func preserveFilterClassesUncached(top, all []datastore.Results) []datastore.Results {
	bestHuman, bestDog := -1, -1
	var bestHumanConf, bestDogConf float32
	for i := range all {
		conf := all[i].Confidence
		human, dog := vocalization.Classify(all[i].Species)
		if human && (bestHuman == -1 || conf > bestHumanConf) {
			bestHuman, bestHumanConf = i, conf
		}
		if dog && (bestDog == -1 || conf > bestDogConf) {
			bestDog, bestDogConf = i, conf
		}
	}
	top = appendIfAbsent(top, all, bestHuman)
	return appendIfAbsent(top, all, bestDog)
}

// TestPreserveFilterClasses_CachedMatchesUncached runs the cached scan against the
// uncached reference over every committed BirdNET label locale, several times per
// locale so both the cold (classifying) and warm (cached) paths are compared.
func TestPreserveFilterClasses_CachedMatchesUncached(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("data", "labels", "V2.4", "*.txt"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	const rounds = 3
	r := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // deterministic test data
	for _, file := range files {
		content, err := os.ReadFile(file)
		require.NoError(t, err)
		labels := strings.Split(strings.TrimRight(string(content), "\n"), "\n")

		t.Run(filepath.Base(file), func(t *testing.T) {
			all := make([]datastore.Results, len(labels))
			for round := range rounds {
				for i := range all {
					all[i] = datastore.Results{Species: labels[i], Confidence: r.Float32()}
				}
				top := all[:defaultTopKResults]
				want := preserveFilterClassesUncached(slices.Clip(slices.Clone(top)), all)
				got := preserveFilterClasses(slices.Clip(slices.Clone(top)), all)
				assert.Equal(t, want, got, "round %d", round)
			}
		})
	}
}

// seedFilterClassCache replaces the process-wide filterClassCache with one holding
// n numeric keys and restores the original when the test ends. The tests using it
// swap shared state, so they must not call t.Parallel.
func seedFilterClassCache(t *testing.T, n int) {
	t.Helper()
	filterClassCache.Lock()
	saved := filterClassCache.m
	filterClassCache.m = make(map[string]filterClass, n)
	for i := range n {
		filterClassCache.m[strconv.Itoa(i)] = 0
	}
	filterClassCache.Unlock()
	t.Cleanup(func() {
		filterClassCache.Lock()
		filterClassCache.m = saved
		filterClassCache.Unlock()
	})
}

// TestStoreFilterClasses_ResetsWhenFull checks the cache starts over instead of
// growing past maxFilterClassCacheEntries.
func TestStoreFilterClasses_ResetsWhenFull(t *testing.T) {
	seedFilterClassCache(t, maxFilterClassCacheEntries)

	storeFilterClasses(map[string]filterClass{"Dog_Dog": filterClassDog})

	filterClassCache.RLock()
	defer filterClassCache.RUnlock()
	assert.Len(t, filterClassCache.m, 1)
	assert.Equal(t, filterClassDog, filterClassCache.m["Dog_Dog"])
}

// TestStoreFilterClasses_OversizedMissesStayBounded checks a single set of misses
// larger than the bound leaves the cache within it rather than storing them all.
func TestStoreFilterClasses_OversizedMissesStayBounded(t *testing.T) {
	seedFilterClassCache(t, maxFilterClassCacheEntries)

	misses := make(map[string]filterClass, maxFilterClassCacheEntries+1)
	for i := range maxFilterClassCacheEntries + 1 {
		misses["new-"+strconv.Itoa(i)] = filterClassHuman
	}
	storeFilterClasses(misses)

	filterClassCache.RLock()
	defer filterClassCache.RUnlock()
	require.Len(t, filterClassCache.m, 1, "the last label restarts a cache that the previous ones filled")
	for label, fc := range filterClassCache.m {
		assert.True(t, strings.HasPrefix(label, "new-"), "only a new label may remain, got %q", label)
		assert.Equal(t, filterClassHuman, fc)
	}
}

// TestStoreFilterClasses_FillsToBoundBeforeRestarting checks the cache keeps every
// entry up to maxFilterClassCacheEntries and starts over only for the label that
// would exceed it.
func TestStoreFilterClasses_FillsToBoundBeforeRestarting(t *testing.T) {
	seedFilterClassCache(t, maxFilterClassCacheEntries-1)

	storeFilterClasses(map[string]filterClass{"Dog_Dog": filterClassDog})

	filterClassCache.RLock()
	assert.Len(t, filterClassCache.m, maxFilterClassCacheEntries, "a cache below the bound keeps every entry")
	assert.Contains(t, filterClassCache.m, "0", "a seeded entry survives while the bound is not exceeded")
	assert.Equal(t, filterClassDog, filterClassCache.m["Dog_Dog"])
	filterClassCache.RUnlock()

	storeFilterClasses(map[string]filterClass{"Speech": filterClassHuman})

	filterClassCache.RLock()
	defer filterClassCache.RUnlock()
	assert.Len(t, filterClassCache.m, 1, "the label past the bound restarts the cache")
	assert.Equal(t, filterClassHuman, filterClassCache.m["Speech"])
}

// TestStoreFilterClasses_KnownLabelsDoNotReset checks that misses another window
// already stored do not count toward the bound, so a full cache is not reset by
// labels it already holds.
func TestStoreFilterClasses_KnownLabelsDoNotReset(t *testing.T) {
	seedFilterClassCache(t, maxFilterClassCacheEntries)

	storeFilterClasses(map[string]filterClass{"0": 0, "1": 0})

	filterClassCache.RLock()
	defer filterClassCache.RUnlock()
	assert.Len(t, filterClassCache.m, maxFilterClassCacheEntries)
}

// TestPreserveFilterClasses_Concurrent runs the scan from several goroutines with
// distinct label sets, as concurrently running models do, so the race detector
// covers the shared cache's cold and warm paths.
func TestPreserveFilterClasses_Concurrent(t *testing.T) {
	const (
		workers    = 8
		iterations = 20
		labelCount = 200
	)
	var wg sync.WaitGroup
	for w := range workers {
		all := make([]datastore.Results, labelCount)
		for i := range all {
			all[i] = datastore.Results{Species: fmt.Sprintf("Concurrent species %d_%d", w, i), Confidence: 0.5}
		}
		all[labelCount-1] = datastore.Results{Species: "Dog_Dog", Confidence: 0.1}
		wg.Go(func() {
			for range iterations {
				got := preserveFilterClasses(slices.Clip(slices.Clone(all[:defaultTopKResults])), all)
				assert.Len(t, got, defaultTopKResults+1)
			}
		})
	}
	wg.Wait()
}
