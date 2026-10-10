package apicore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMatchIfNoneMatch covers the conditional-request matcher directly,
// including the wildcard, comma-separated lists, the weak-validator form a proxy
// may send, and whitespace/degenerate candidates. Relocated here from the models
// domain when the matcher was consolidated onto apicore.
func TestMatchIfNoneMatch(t *testing.T) {
	t.Parallel()
	const etag = `"abc123"`
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"empty header", "", false},
		{"exact strong match", `"abc123"`, true},
		{"wildcard", "*", true},
		{"wildcard with surrounding spaces", "  *  ", true},
		{"weak-validator form", `W/"abc123"`, true},
		{"comma list contains match", `"nope", "abc123"`, true},
		{"comma list contains weak match", `W/"x", W/"abc123"`, true},
		{"comma list no match", `"nope", "nada"`, false},
		{"leading/trailing spaces around single tag", `  "abc123"  `, true},
		{"no match", `"different"`, false},
		{"prefix-only, not a real tag", `"abc"`, false},
		{"lone weak prefix does not match", "W/", false},
		{"trailing comma leaves empty candidate", `"abc123",`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, MatchIfNoneMatch(tc.header, etag), "header %q", tc.header)
		})
	}
}
