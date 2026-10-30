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

// TestMatchIfMatch pins the strong comparison If-Match requires (RFC 9110
// section 13.1.1): the wildcard and list forms match, a weak validator never
// does, and an empty header never matches. The settings write path turns a false
// result into a 412, so a parsing regression here either rejects valid saves or
// lets a stale one through.
func TestMatchIfMatch(t *testing.T) {
	t.Parallel()
	const etag = `"epoch-7"`
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"empty header", "", false},
		{"exact strong match", `"epoch-7"`, true},
		{"wildcard", "*", true},
		{"wildcard with surrounding spaces", "  *  ", true},
		{"comma list contains match", `"epoch-6", "epoch-7"`, true},
		{"comma list no match", `"epoch-6", "epoch-8"`, false},
		{"surrounding spaces around single tag", `  "epoch-7"  `, true},
		{"different revision", `"epoch-6"`, false},
		{"unquoted value", "epoch-7", false},
		{"weak validator never matches", `W/"epoch-7"`, false},
		{"weak validator in a list does not match", `W/"epoch-7", "epoch-6"`, false},
		{"weak validator next to the strong one still matches the strong one", `W/"epoch-6", "epoch-7"`, true},
		{"lone weak prefix", "W/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, MatchIfMatch(tc.header, etag), "header %q", tc.header)
		})
	}
}
