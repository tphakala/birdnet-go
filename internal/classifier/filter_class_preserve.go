package classifier

import (
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/labels/vocalization"
)

// maxPreservedFilterClasses is the most results preserveFilterClasses can append
// past the top-K cut: one human class and one dog class. getTopKResults pre-sizes
// its output slice by this amount so the appends never reallocate; keep the two in
// sync.
const maxPreservedFilterClasses = 2

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
// A single pass over all classifies each label once via vocalization.Classify
// (this runs per inference window over the whole ~6.5k-15k prediction set, so the
// single lowercase-once classification matters). The appended datastore.Results
// values are pure value types (datastore.Results in internal/datastore/model.go
// has only uint/string/float32 fields), so copying them by value preserves the
// race-safety contract getTopKResults relies on for the reused BirdNET scratch
// buffer.
func preserveFilterClasses(top, all []datastore.Results) []datastore.Results {
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
