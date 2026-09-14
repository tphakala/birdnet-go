// sections_sync_test.go enforces that the canonical heading vocabulary in
// sections.go stays identical to its twin in the frontend classifier. The two
// lists are ~130 character-for-character copies across 16 locales, and they use
// different matching semantics (Go: leading word boundary; TS: plain substring),
// so a one-sided edit produces no compile error and no visible failure — the
// backend simply stops promoting a sub-section the frontend would have shown,
// or promotes one the frontend then drops as a duplicate row. Nothing else pins
// this; the source comments on both sides only ask nicely.
package guideprovider

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// speciesTSPath is the frontend classifier, relative to this package directory.
const speciesTSPath = "../../frontend/src/lib/types/species.ts"

// The patterns anchor on a literal leading newline rather than `^` with the `m`
// flag: both express "at the start of a line", but the explicit newline reads
// unambiguously and keeps gocritic's badRegexp check quiet. readFrontendSource
// prepends one so a declaration at offset 0 would still match.
var (
	// Matches `export const NAME = [ ... ];` with the closing bracket at column 0.
	tsArrayPattern = regexp.MustCompile(`(?s)\nexport const (GUIDE_\w+_HEADINGS)\s*=\s*\[(.*?)\n\];`)
	// Matches a single-quoted token. No vocabulary entry contains an apostrophe.
	tsTokenPattern = regexp.MustCompile(`'([^']*)'`)
	// Matches the frontend's classifyCanonicalHeading body, then the vocabulary
	// list each of its branches consults, in source order.
	tsClassifyPattern = regexp.MustCompile(`(?s)\nexport function classifyCanonicalHeading\(.*?\n\}`)
	tsVocabRefPattern = regexp.MustCompile(`matchesHeading\(\w+,\s*(GUIDE_\w+_HEADINGS)\)`)
)

// readFrontendSource returns the frontend classifier with a leading newline, so
// the line-anchored patterns above match a declaration at the very start of the
// file as readily as any other.
func readFrontendSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(speciesTSPath))
	require.NoError(t, err, "the frontend classifier must be readable from the repo checkout")
	return "\n" + string(raw)
}

// parseTSHeadingLists extracts every `export const GUIDE_*_HEADINGS` array from
// the frontend source, preserving element order. Comment lines are skipped so a
// locale marker like `// cs / sk` cannot be mistaken for a token.
func parseTSHeadingLists(t *testing.T, source string) map[string][]string {
	t.Helper()
	lists := make(map[string][]string)
	for _, match := range tsArrayPattern.FindAllStringSubmatch(source, -1) {
		name, body := match[1], match[2]
		var tokens []string
		for line := range strings.Lines(body) {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, tok := range tsTokenPattern.FindAllStringSubmatch(trimmed, -1) {
				tokens = append(tokens, tok[1])
			}
		}
		lists[name] = tokens
	}
	return lists
}

func TestHeadingVocabulariesMatchFrontend(t *testing.T) {
	t.Parallel()

	lists := parseTSHeadingLists(t, readFrontendSource(t))

	// Guard the parser itself: a regex that silently matched nothing would make
	// every comparison below trivially pass against an empty list.
	require.Len(t, lists, 4, "expected exactly four GUIDE_*_HEADINGS arrays, got %v", lists)

	for _, tc := range []struct {
		tsName string
		goList []string
	}{
		{"GUIDE_SONGS_HEADINGS", guideSongsHeadings},
		{"GUIDE_APPEARANCE_HEADINGS", guideAppearanceHeadings},
		{"GUIDE_HABITAT_HEADINGS", guideHabitatHeadings},
		{"GUIDE_BEHAVIOUR_HEADINGS", guideBehaviourHeadings},
	} {
		t.Run(tc.tsName, func(t *testing.T) {
			t.Parallel()
			tsList, ok := lists[tc.tsName]
			require.True(t, ok, "%s not found in %s", tc.tsName, speciesTSPath)
			require.NotEmpty(t, tsList, "%s parsed as empty", tc.tsName)
			assert.Equal(t, tc.goList, tsList,
				"%s and its Go twin in sections.go have drifted; edit both", tc.tsName)
		})
	}
}

// TestClassifyCheckOrderMatchesFrontend pins the order in which the two
// classifiers consult the vocabularies. The lists overlap (a heading like
// "Song and habitat" matches two), so first-match-wins order decides the
// category — and the two sides must decide the same way.
func TestClassifyCheckOrderMatchesFrontend(t *testing.T) {
	t.Parallel()

	fn := tsClassifyPattern.FindString(readFrontendSource(t))
	require.NotEmpty(t, fn, "classifyCanonicalHeading not found in %s", speciesTSPath)

	refs := tsVocabRefPattern.FindAllStringSubmatch(fn, -1)
	tsOrder := make([]string, 0, len(refs))
	for _, m := range refs {
		tsOrder = append(tsOrder, m[1])
	}
	// The Go order is the switch in classifyCanonicalHeading (sections.go).
	goOrder := []string{
		"GUIDE_APPEARANCE_HEADINGS",
		"GUIDE_SONGS_HEADINGS",
		"GUIDE_HABITAT_HEADINGS",
		"GUIDE_BEHAVIOUR_HEADINGS",
	}
	assert.Equal(t, goOrder, tsOrder,
		"the frontend checks the vocabularies in a different order than classifyCanonicalHeading in sections.go")
}
