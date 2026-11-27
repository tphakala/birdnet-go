// species_guide_limits_sync_test.go enforces that the species-guide warm-target
// bounds stay identical to their twin in the frontend. The frontend restates them
// because it validates and renders the same slider the backend clamps, and the two
// sides have no shared source: a change to either constant alone produces no
// compile error and no test failure anywhere else — the UI would simply offer a
// range the backend silently clamps, or default a fresh install to a value the
// backend does not.
package conf

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// speciesGuideLimitsTSPath is the frontend constants module, relative to this
// package directory.
const speciesGuideLimitsTSPath = "../../frontend/src/lib/utils/speciesGuideLimits.ts"

// Matches `export const NAME = 123;`, capturing the name and the literal.
var tsNumberConstPattern = regexp.MustCompile(`export const (SPECIES_GUIDE_\w+)\s*=\s*(-?\d+);`)

func TestSpeciesGuideWarmLimitsMatchFrontend(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Clean(speciesGuideLimitsTSPath))
	require.NoError(t, err, "the frontend species-guide limits module must be readable from the repo checkout")

	found := make(map[string]int)
	for _, m := range tsNumberConstPattern.FindAllStringSubmatch(string(raw), -1) {
		v, convErr := strconv.Atoi(m[2])
		require.NoErrorf(t, convErr, "%s is not an integer literal", m[1])
		found[m[1]] = v
	}
	// Guard against the patterns silently matching nothing (a rename or a
	// reformat), which would otherwise make every assertion below vacuous.
	require.Len(t, found, 3, "expected exactly three exported limits, got %v", found)

	want := map[string]int{
		"SPECIES_GUIDE_MIN_WARM_TOP_N":     0,
		"SPECIES_GUIDE_MAX_WARM_TOP_N":     SpeciesGuideMaxWarmTopN,
		"SPECIES_GUIDE_DEFAULT_WARM_TOP_N": SpeciesGuideDefaultWarmTopN,
	}
	for name, expected := range want {
		actual, ok := found[name]
		if !assert.Truef(t, ok, "%s is missing from the frontend limits module", name) {
			continue
		}
		assert.Equalf(t, expected, actual, "%s has drifted from its Go counterpart", name)
	}
}
