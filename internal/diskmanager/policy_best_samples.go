// policy_best_samples.go - confidence-ranked clip retention policy
package diskmanager

import (
	"sort"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// BestSamplesCleanup retains all clips within the configured MaxAge window and
// the highest-confidence MinClips samples per species beyond that window.
func BestSamplesCleanup(quit <-chan struct{}, db Interface) CleanupResult {
	return cleanupByAgePolicy(quit, db, conf.RetentionPolicyBestSamples)
}

// selectBestClipsPerSpecies returns paths for the highest-confidence clips to
// keep for each species. Confidence is the primary ranking; ties prefer newer
// clips, then lexicographically smaller paths for deterministic selection.
func selectBestClipsPerSpecies(files []FileInfo, minClipsPerSpecies int) map[string]struct{} {
	bestClipPaths := make(map[string]struct{})
	if minClipsPerSpecies <= 0 {
		return bestClipPaths
	}

	indicesBySpecies := make(map[string][]int)
	for i := range files {
		indicesBySpecies[files[i].Species] = append(indicesBySpecies[files[i].Species], i)
	}

	for _, indices := range indicesBySpecies {
		sort.Slice(indices, func(i, j int) bool {
			left, right := files[indices[i]], files[indices[j]]
			if left.Confidence != right.Confidence {
				return left.Confidence > right.Confidence
			}
			if !left.Timestamp.Equal(right.Timestamp) {
				return left.Timestamp.After(right.Timestamp)
			}
			return left.Path < right.Path
		})

		limit := min(minClipsPerSpecies, len(indices))
		for _, index := range indices[:limit] {
			bestClipPaths[files[index].Path] = struct{}{}
		}
	}

	return bestClipPaths
}
