package support

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyAddressEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry string
		want  string
	}{
		{"preset lowercase", "cloudflare", "cloudflare"},
		{"preset capitalised", "Cloudflare", "cloudflare"},
		{"preset padded upper", " CLOUDFLARE ", "cloudflare"},
		{"preset lookalike host", "cloudflare.example.com", "[redacted]"},
		{"preset in list string", "cloudflare,192.0.2.1", "[redacted]"},
		{"not an address", "not-an-ip", "[redacted]"},
		{"bad prefix length", "192.0.2.0/33", "[redacted]"},
		{"loopback v4", "127.0.0.1", "[redacted loopback address]"},
		{"loopback v6", "::1", "[redacted loopback address]"},
		{"loopback range", "127.0.0.0/8", "[redacted loopback range]"},
		{"private range", "10.0.0.0/8", "[redacted private range]"},
		{"private range host bits", "192.168.1.5/24", "[redacted private range]"},
		{"private v4 address", "172.18.0.5", "[redacted private address]"},
		{"private v6 address", "fd00::1", "[redacted private address]"},
		{"mapped private", "::ffff:10.1.2.3", "[redacted private address]"},
		{"link-local v4", "169.254.1.1", "[redacted link-local address]"},
		{"link-local v6 zone", "fe80::1%eth0", "[redacted link-local address]"},
		{"unspecified single prefix", "0.0.0.0/32", "[redacted unspecified address]"},
		{"trust-all v4", "0.0.0.0/0", "[redacted public range]"},
		{"trust-all v6", "::/0", "[redacted public range]"},
		{"range straddling private and public", "10.0.0.0/7", "[redacted public range]"},
		{"range starting outside loopback", "127.0.0.0/4", "[redacted public range]"},
		{"unaligned private range stays private", "172.16.0.0/12", "[redacted private range]"},
		{"private v6 range", "fd00::/8", "[redacted private range]"},
		{"cgnat address", "100.64.0.1", "[redacted public address]"},
		{"cgnat range", "100.64.0.0/10", "[redacted public range]"},
		{"unspecified address", "::", "[redacted unspecified address]"},
		{"public address", "203.0.113.7", "[redacted public address]"},
		{"public single prefix", "203.0.113.7/32", "[redacted public address]"},
		{"public v4 range", "198.51.100.0/24", "[redacted public range]"},
		{"public v6 range", "2001:db8::/32", "[redacted public range]"},
		{"empty", "", ""},
		{"whitespace", "   ", "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, classifyAddressEntry(tt.entry))
		})
	}
}

// FuzzClassifyAddressEntry pins that no input string, other than empty entries
// and the preset, can appear in the output.
func FuzzClassifyAddressEntry(f *testing.F) {
	seeds := []string{
		"cloudflare", "Cloudflare", " CLOUDFLARE ", "cloudflare.example.com",
		"cloudflare,192.0.2.1", "not-an-ip", "192.0.2.0/33", "127.0.0.1", "::1",
		"127.0.0.0/8", "10.0.0.0/8", "192.168.1.5/24", "172.18.0.5", "fd00::1",
		"::ffff:10.1.2.3", "169.254.1.1", "fe80::1%eth0", "0.0.0.0/0", "::",
		"203.0.113.7", "203.0.113.7/32", "198.51.100.0/24", "2001:db8::/32", "", "   ",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	allowed := map[string]bool{redactedPlaceholder: true}
	for _, scope := range []string{
		addressScopeLoopback, addressScopePrivate, addressScopeLinkLocal,
		addressScopeUnspecified, addressScopePublic,
	} {
		for _, kind := range []string{addressKindAddress, addressKindRange} {
			allowed[formatRedactedAddress(scope, kind)] = true
		}
	}

	f.Fuzz(func(t *testing.T, in string) {
		out := classifyAddressEntry(in)
		trimmed := strings.TrimSpace(in)
		switch {
		case trimmed == "":
			assert.Equal(t, in, out)
		case strings.EqualFold(trimmed, "cloudflare"):
			assert.Equal(t, "cloudflare", out)
		default:
			// The closed output set is the invariant: no input text can appear.
			assert.True(t, allowed[out], "unexpected output %q for %q", out, in)
		}
	})
}

func TestRedactAddressList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  any
	}{
		{"nil", nil, nil},
		{"empty string", "", ""},
		{"scalar address", "198.51.100.20", "[redacted public address]"},
		{"scalar preset", "cloudflare", "cloudflare"},
		{"map item is blanked whole", []any{map[string]any{"ip": "203.0.113.7"}}, []any{"[redacted]"}},
		{"number and nil items", []any{42, nil}, []any{"[redacted]", "[redacted]"}},
		{"nested list item", []any{[]any{"203.0.113.7"}}, []any{"[redacted]"}},
		{"map as the value", map[string]any{"203.0.113.7": true}, "[redacted]"},
		{"mixed keeps order", []any{"10.0.0.1", 7, "cloudflare"}, []any{"[redacted private address]", "[redacted]", "cloudflare"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, redactAddressList(tt.value))
		})
	}
}
