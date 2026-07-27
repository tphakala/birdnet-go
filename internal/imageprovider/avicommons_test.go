// avicommons_test.go: unit tests for Avicommons license code normalization.
package imageprovider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapAviCommonsLicense covers the historical slug-style license codes, the
// display-name variants and the jurisdiction-ported codes observed in
// production data. The version a code carries must survive into both the
// display name and the deed URL: every non-4.0 entry used to render as 4.0
// with a link to legal text that does not govern it, and CC 2.0/3.0 differ
// from 4.0 on attribution, ShareAlike compatibility and the cure period.
//
// Note: this test intentionally does NOT call t.Parallel() because
// mapAviCommonsLicense mutates the package-global loggedUnknownLicenses
// sync.Map via LoadOrStore when it encounters an unknown code. Per the
// project coding guideline, tests that mutate global state must run
// sequentially.
func TestMapAviCommonsLicense(t *testing.T) {
	const (
		cc0Name         = "CC0 1.0 Universal"
		cc0URL          = "https://creativecommons.org/publicdomain/zero/1.0/"
		unknownLicense  = "completely-bogus"
		unknownLicense2 = ""
	)

	tests := []struct {
		name     string
		input    string
		wantName string
		wantURL  string
	}{
		// Legacy slug-style codes carry no version, so none is asserted. The
		// version-neutral license page is the honest link for them.
		{name: "slug cc-by", input: "cc-by", wantName: "CC BY", wantURL: "https://creativecommons.org/licenses/by/"},
		{name: "slug cc-by-sa", input: "cc-by-sa", wantName: "CC BY-SA", wantURL: "https://creativecommons.org/licenses/by-sa/"},
		{name: "slug cc-by-nd", input: "cc-by-nd", wantName: "CC BY-ND", wantURL: "https://creativecommons.org/licenses/by-nd/"},
		{name: "slug cc-by-nc", input: "cc-by-nc", wantName: "CC BY-NC", wantURL: "https://creativecommons.org/licenses/by-nc/"},
		{name: "slug cc-by-nc-sa", input: "cc-by-nc-sa", wantName: "CC BY-NC-SA", wantURL: "https://creativecommons.org/licenses/by-nc-sa/"},
		{name: "slug cc-by-nc-nd", input: "cc-by-nc-nd", wantName: "CC BY-NC-ND", wantURL: "https://creativecommons.org/licenses/by-nc-nd/"},
		{name: "slug cc0", input: "cc0", wantName: cc0Name, wantURL: cc0URL},

		// Production display-name variants. The version must be preserved: these
		// used to be reported as 4.0 whatever they said.
		{name: "display CC BY 3.0", input: "CC BY 3.0", wantName: "CC BY 3.0", wantURL: "https://creativecommons.org/licenses/by/3.0/"},
		{name: "display CC BY-NC 2.0", input: "CC BY-NC 2.0", wantName: "CC BY-NC 2.0", wantURL: "https://creativecommons.org/licenses/by-nc/2.0/"},
		{name: "display CC BY-NC 2.5", input: "CC BY-NC 2.5", wantName: "CC BY-NC 2.5", wantURL: "https://creativecommons.org/licenses/by-nc/2.5/"},
		{name: "display CC BY-NC-SA 4.0", input: "CC BY-NC-SA 4.0", wantName: "CC BY-NC-SA 4.0", wantURL: "https://creativecommons.org/licenses/by-nc-sa/4.0/"},
		{name: "display CC BY-SA 4.0", input: "CC BY-SA 4.0", wantName: "CC BY-SA 4.0", wantURL: "https://creativecommons.org/licenses/by-sa/4.0/"},
		{name: "display CC BY-NC 1.0", input: "CC BY-NC 1.0", wantName: "CC BY-NC 1.0", wantURL: "https://creativecommons.org/licenses/by-nc/1.0/"},

		// Jurisdiction-ported codes. These used to defeat the version strip
		// entirely and render the raw code with no license URL at all.
		{name: "ported CC BY-NC 3.0-de", input: "CC BY-NC 3.0-de", wantName: "CC BY-NC 3.0 DE", wantURL: "https://creativecommons.org/licenses/by-nc/3.0/de/"},
		{name: "ported CC BY 3.0-us", input: "CC BY 3.0-us", wantName: "CC BY 3.0 US", wantURL: "https://creativecommons.org/licenses/by/3.0/us/"},
		{name: "ported CC BY-SA 3.0-nz", input: "CC BY-SA 3.0-nz", wantName: "CC BY-SA 3.0 NZ", wantURL: "https://creativecommons.org/licenses/by-sa/3.0/nz/"},
		{name: "ported CC BY-NC 2.5-br", input: "CC BY-NC 2.5-br", wantName: "CC BY-NC 2.5 BR", wantURL: "https://creativecommons.org/licenses/by-nc/2.5/br/"},

		// CC0 exists only as 1.0 and was never ported, so the versions and ports
		// the dataset carries for it name no real license.
		{name: "display CC0 3.0", input: "CC0 3.0", wantName: cc0Name, wantURL: cc0URL},
		{name: "ported CC0 2.5-au", input: "CC0 2.5-au", wantName: cc0Name, wantURL: cc0URL},

		// Edge cases.
		{name: "whitespace around CC BY", input: "  CC BY  ", wantName: "CC BY", wantURL: "https://creativecommons.org/licenses/by/"},
		{name: "unpublished version drops the version", input: "cc-by 99.99", wantName: "CC BY", wantURL: "https://creativecommons.org/licenses/by/"},
		{name: "mixed case CC-BY-NC", input: "CC-BY-NC", wantName: "CC BY-NC", wantURL: "https://creativecommons.org/licenses/by-nc/"},

		// Unknowns: raw code returned as name, URL empty. The one-shot WARN
		// logging path is exercised implicitly; we just assert the return.
		{name: "unknown code", input: unknownLicense, wantName: unknownLicense, wantURL: ""},
		{name: "empty string", input: unknownLicense2, wantName: unknownLicense2, wantURL: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No t.Parallel(): see note on the outer test.
			gotName, gotURL := mapAviCommonsLicense(tt.input)
			assert.Equal(t, tt.wantName, gotName, "license name mismatch for input %q", tt.input)
			assert.Equal(t, tt.wantURL, gotURL, "license URL mismatch for input %q", tt.input)
		})
	}
}

// TestParseAviCommonsLicense pins how a raw license string is split, in
// particular that a jurisdiction port is only recognized behind a version so
// the "nd" of "cc-by-nd" is not mistaken for one.
func TestParseAviCommonsLicense(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantFamily  string
		wantVersion string
		wantPort    string
	}{
		{name: "already normalized", input: "cc-by-nc", wantFamily: "cc-by-nc"},
		{name: "display variant", input: "CC BY 3.0", wantFamily: "cc-by", wantVersion: "3.0"},
		{name: "display compound", input: "CC BY-NC-SA 4.0", wantFamily: "cc-by-nc-sa", wantVersion: "4.0"},
		{name: "zero with version", input: "CC0 3.0", wantFamily: "cc0", wantVersion: "3.0"},
		{name: "extra whitespace", input: "  CC BY  ", wantFamily: "cc-by"},
		{name: "no version", input: "CC BY", wantFamily: "cc-by"},
		{name: "spaces collapsed", input: "cc  by  nc", wantFamily: "cc-by-nc"},
		{name: "no-derivatives is not a jurisdiction", input: "cc-by-nd", wantFamily: "cc-by-nd"},
		{name: "ported", input: "CC BY-NC 3.0-de", wantFamily: "cc-by-nc", wantVersion: "3.0", wantPort: "de"},
		{name: "ported no-derivatives", input: "CC BY-NC-ND 3.0-de", wantFamily: "cc-by-nc-nd", wantVersion: "3.0", wantPort: "de"},
		{name: "unknown preserved", input: "completely-bogus", wantFamily: "completely-bogus"},
		{name: "empty", input: "", wantFamily: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			family, version, port := parseAviCommonsLicense(tt.input)
			assert.Equal(t, tt.wantFamily, family, "family mismatch for input %q", tt.input)
			assert.Equal(t, tt.wantVersion, version, "version mismatch for input %q", tt.input)
			assert.Equal(t, tt.wantPort, port, "port mismatch for input %q", tt.input)
		})
	}
}

// TestMapAviCommonsLicense_CoversTheShippedDataset walks every distinct license
// string in the embedded dataset and requires each to produce a deed URL.
//
// This is the assertion the jurisdiction-ported codes needed: 22 entries use
// codes like "CC BY-NC 3.0-de", whose trailing "-de" defeated the version strip,
// so they fell through to the unknown branch and rendered the raw code with no
// license URL at all, plus one Warn per unique code straight into Sentry.
func TestMapAviCommonsLicense_CoversTheShippedDataset(t *testing.T) {
	// No t.Parallel(): mapAviCommonsLicense mutates the package-global
	// loggedUnknownLicenses, the same reason the sibling test above gives.

	// data/latest.json inside THIS package is the file main.go embeds
	// (//go:embed internal/imageprovider/data/latest.json). The repo-root copy
	// is a different, larger file that nothing ships, so asserting against it
	// would guard data the binary never sees. Read directly rather than through
	// internal/api's embed, which imports this package.
	data, err := os.ReadFile(filepath.Join("data", "latest.json"))
	require.NoError(t, err, "the embedded Avicommons dataset must be readable")

	var entries []struct {
		License string `json:"license"`
	}
	require.NoError(t, json.Unmarshal(data, &entries))
	require.NotEmpty(t, entries)

	seen := make(map[string]bool)
	for i := range entries {
		code := entries[i].License
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true

		name, url := mapAviCommonsLicense(code)
		assert.NotEmpty(t, name, "license %q produced no display name", code)
		assert.NotEmpty(t, url, "license %q produced no deed URL", code)
	}
	require.NotEmpty(t, seen, "the dataset should carry license codes")
}
