// avicommons_test.go: unit tests for Avicommons license code normalization.
package imageprovider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMapAviCommonsLicense covers both the historical slug-style license
// codes and the display-name variants observed in production data. The
// normalization helper should fold things like "CC BY 3.0" onto the same
// canonical "cc-by" case so the switch returns proper names and URLs.
//
// Note: this test intentionally does NOT call t.Parallel() because
// mapAviCommonsLicense mutates the package-global loggedUnknownLicenses
// sync.Map via LoadOrStore when it encounters an unknown code. Per the
// project coding guideline, tests that mutate global state must run
// sequentially.
func TestMapAviCommonsLicense(t *testing.T) {
	const (
		ccBYName        = "CC BY 4.0"
		ccBYURL         = "https://creativecommons.org/licenses/by/4.0/"
		ccBYSAName      = "CC BY-SA 4.0"
		ccBYSAURL       = "https://creativecommons.org/licenses/by-sa/4.0/"
		ccBYNDName      = "CC BY-ND 4.0"
		ccBYNDURL       = "https://creativecommons.org/licenses/by-nd/4.0/"
		ccBYNCName      = "CC BY-NC 4.0"
		ccBYNCURL       = "https://creativecommons.org/licenses/by-nc/4.0/"
		ccBYNCSAName    = "CC BY-NC-SA 4.0"
		ccBYNCSAURL     = "https://creativecommons.org/licenses/by-nc-sa/4.0/"
		ccBYNCNDName    = "CC BY-NC-ND 4.0"
		ccBYNCNDURL     = "https://creativecommons.org/licenses/by-nc-nd/4.0/"
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
		// Legacy slug-style codes — must continue to work unchanged.
		{name: "slug cc-by", input: "cc-by", wantName: ccBYName, wantURL: ccBYURL},
		{name: "slug cc-by-sa", input: "cc-by-sa", wantName: ccBYSAName, wantURL: ccBYSAURL},
		{name: "slug cc-by-nd", input: "cc-by-nd", wantName: ccBYNDName, wantURL: ccBYNDURL},
		{name: "slug cc-by-nc", input: "cc-by-nc", wantName: ccBYNCName, wantURL: ccBYNCURL},
		{name: "slug cc-by-nc-sa", input: "cc-by-nc-sa", wantName: ccBYNCSAName, wantURL: ccBYNCSAURL},
		{name: "slug cc-by-nc-nd", input: "cc-by-nc-nd", wantName: ccBYNCNDName, wantURL: ccBYNCNDURL},
		{name: "slug cc0", input: "cc0", wantName: cc0Name, wantURL: cc0URL},

		// Production display-name variants that used to fall through to the
		// WARN-logging default branch (Forgejo #387).
		{name: "display CC BY 3.0", input: "CC BY 3.0", wantName: ccBYName, wantURL: ccBYURL},
		{name: "display CC BY-NC 2.0", input: "CC BY-NC 2.0", wantName: ccBYNCName, wantURL: ccBYNCURL},
		{name: "display CC BY-NC 3.0", input: "CC BY-NC 3.0", wantName: ccBYNCName, wantURL: ccBYNCURL},
		{name: "display CC BY-NC-SA 4.0", input: "CC BY-NC-SA 4.0", wantName: ccBYNCSAName, wantURL: ccBYNCSAURL},
		{name: "display CC BY-SA 4.0", input: "CC BY-SA 4.0", wantName: ccBYSAName, wantURL: ccBYSAURL},
		{name: "display CC0 3.0", input: "CC0 3.0", wantName: cc0Name, wantURL: cc0URL},

		// Edge cases.
		{name: "whitespace around CC BY", input: "  CC BY  ", wantName: ccBYName, wantURL: ccBYURL},
		{name: "high version cc-by-99.99", input: "cc-by 99.99", wantName: ccBYName, wantURL: ccBYURL},
		{name: "mixed case CC-BY-NC", input: "CC-BY-NC", wantName: ccBYNCName, wantURL: ccBYNCURL},

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

// TestNormalizeAviCommonsLicense pins the exact output of the normalization
// helper so future changes don't silently shift the canonical form.
func TestNormalizeAviCommonsLicense(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "already normalized", input: "cc-by-nc", want: "cc-by-nc"},
		{name: "display variant", input: "CC BY 3.0", want: "cc-by"},
		{name: "display compound", input: "CC BY-NC-SA 4.0", want: "cc-by-nc-sa"},
		{name: "zero with version", input: "CC0 3.0", want: "cc0"},
		{name: "extra whitespace", input: "  CC BY  ", want: "cc-by"},
		{name: "no version", input: "CC BY", want: "cc-by"},
		{name: "spaces collapsed", input: "cc  by  nc", want: "cc-by-nc"},
		{name: "unknown preserved", input: "completely-bogus", want: "completely-bogus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, normalizeAviCommonsLicense(tt.input))
		})
	}
}
