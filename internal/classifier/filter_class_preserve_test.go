package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
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
