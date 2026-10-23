package classifier

import (
	"sync"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/labels/vocalization"
)

// maxPreservedFilterClasses is the most results preserveFilterClasses can append
// past the top-K cut: one human class and one dog class. getTopKResults pre-sizes
// its output slice by this amount so the appends never reallocate; keep the two in
// sync.
const maxPreservedFilterClasses = 2

// maxFilterClassCacheEntries bounds filterClassCache. Every loaded model's labels
// together come to roughly 35k (BirdNET v2.4, BirdNET v3, Perch v2 and bat), so
// the cache normally never reaches this; the bound only stops it growing without
// limit if label files are swapped (locale changes, model replacements) many
// times in one run.
const maxFilterClassCacheEntries = 1 << 16

// filterClass is the cached vocalization.Classify verdict for one raw label.
type filterClass uint8

const (
	filterClassHuman filterClass = 1 << iota
	filterClassDog
)

// filterClassCache memoizes vocalization.Classify per raw label. Classify
// lowercases the label, which allocates for every mixed-case label, and
// preserveFilterClasses classifies the whole prediction set on every inference
// window; a model's label set is fixed, so after the first window every lookup
// hits the cache and the scan does not allocate.
//
//nolint:gochecknoglobals // process-wide memo shared by every model instance
var filterClassCache = struct {
	sync.RWMutex
	m map[string]filterClass
}{m: make(map[string]filterClass)}

// classifyFilterClass returns label's human/dog verdict as a filterClass.
func classifyFilterClass(label string) filterClass {
	human, dog := vocalization.Classify(label)
	var fc filterClass
	if human {
		fc |= filterClassHuman
	}
	if dog {
		fc |= filterClassDog
	}
	return fc
}

// storeFilterClasses adds newly classified labels to filterClassCache, starting
// the cache over whenever a label that is not yet cached would take it past
// maxFilterClassCacheEntries, so the bound holds for any size of misses. Labels
// another window stored meanwhile are already cached and never trigger a restart.
// A single set of misses larger than the bound keeps restarting the cache, which
// is slower but still correct.
func storeFilterClasses(misses map[string]filterClass) {
	filterClassCache.Lock()
	defer filterClassCache.Unlock()
	for label, fc := range misses {
		if _, ok := filterClassCache.m[label]; !ok && len(filterClassCache.m) >= maxFilterClassCacheEntries {
			clear(filterClassCache.m)
		}
		filterClassCache.m[label] = fc
	}
}

// preserveFilterClasses ensures the privacy and dog-bark filters still see the
// human and dog classes they depend on after top-K truncation. Those filters run
// in the analysis processor over the results the classifier returns, so a human
// or dog prediction that ranks below the top-K would otherwise be discarded before
// the filter could act on it. BirdNET scores faint human speech near 1-10% while a
// full chunk of louder birds outranks it, so the "Human"/"Speech" prediction is
// routinely cut at the top-K step and never reaches the privacy filter (issue
// #4177).
//
// It appends the single highest-confidence human class and the single
// highest-confidence dog class from the full result set when they are not already
// present in top. The filters only need the strongest such prediction (they gate a
// single map write on it), so at most two entries are added: a saved detection in
// the same chunk therefore gains at most two low-confidence additional results, a
// truthful record of what the model heard.
//
// A single pass over all looks each label up in filterClassCache, classifying it
// with vocalization.Classify only the first time it is seen (this runs per
// inference window over the whole ~6.5k-15k prediction set, so a per-label
// allocation here is ~6.5k-15k allocations per window). The appended datastore.Results
// values are pure value types (datastore.Results in internal/datastore/model.go
// has only uint/string/float32 fields), so copying them by value preserves the
// race-safety contract getTopKResults relies on for the reused BirdNET scratch
// buffer.
func preserveFilterClasses(top, all []datastore.Results) []datastore.Results {
	bestHuman, bestDog := -1, -1
	var bestHumanConf, bestDogConf float32
	var misses map[string]filterClass

	filterClassCache.RLock()
	for i := range all {
		label := all[i].Species
		fc, ok := filterClassCache.m[label]
		if !ok {
			if fc, ok = misses[label]; !ok {
				fc = classifyFilterClass(label)
				if misses == nil {
					misses = make(map[string]filterClass)
				}
				misses[label] = fc
			}
		}
		conf := all[i].Confidence
		if fc&filterClassHuman != 0 && (bestHuman == -1 || conf > bestHumanConf) {
			bestHuman, bestHumanConf = i, conf
		}
		if fc&filterClassDog != 0 && (bestDog == -1 || conf > bestDogConf) {
			bestDog, bestDogConf = i, conf
		}
	}
	filterClassCache.RUnlock()

	if misses != nil {
		storeFilterClasses(misses)
	}

	top = appendIfAbsent(top, all, bestHuman)
	top = appendIfAbsent(top, all, bestDog)
	return top
}

// appendIfAbsent appends all[idx] to top unless a result carrying that same
// Species string already survived truncation into top. idx < 0 (no match found)
// returns top unchanged.
func appendIfAbsent(top, all []datastore.Results, idx int) []datastore.Results {
	if idx < 0 {
		return top
	}
	for i := range top {
		if top[i].Species == all[idx].Species {
			return top // strongest match already survived truncation
		}
	}
	return append(top, all[idx])
}
