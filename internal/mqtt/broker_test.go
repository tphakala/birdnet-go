// broker_test.go: tests for the canonical MQTT broker-address parser.

package mqtt

import (
	"net"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseBroker verifies scheme/host/port extraction across scheme-less,
// scheme-prefixed, IPv6, and malformed broker addresses. The scheme-less
// host:port forms are the regression cases for the original TLS bug.
func TestParseBroker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		broker     string
		wantScheme string
		wantHost   string
		wantPort   string
		wantErr    bool
	}{
		// Scheme-less forms (the regression cases).
		{"scheme-less hostname with port", "mybroker:1883", "", "mybroker", "1883", false},
		{"scheme-less IPv4 with port", "192.168.1.5:1883", "", "192.168.1.5", "1883", false},
		{"scheme-less bracketed IPv6 with port", "[::1]:1883", "", "::1", "1883", false},
		{"scheme-less bare hostname", "mybroker", "", "mybroker", "", false},
		{"scheme-less bare IPv4", "192.168.1.5", "", "192.168.1.5", "", false},
		{"scheme-less bare IPv6", "2001:db8::1", "", "2001:db8::1", "", false},
		// The "::" guard must keep bare IPv6 from being mistaken for a ":port".
		{"bare IPv6 loopback not localhost-prefixed", "::1", "", "::1", "", false},
		{"bare IPv6 unspecified", "::", "", "::", "", false},
		{"bracketed IPv6 without port", "[::1]", "", "::1", "", false},
		{"leading colon implies localhost", ":1883", "", "127.0.0.1", "1883", false},
		{"empty broker", "", "", "", "", false},
		{"surrounding whitespace trimmed", "  tcp://host:1883  ", "tcp", "host", "1883", false},

		// Scheme-prefixed forms.
		{"tcp scheme", "tcp://mybroker:1883", "tcp", "mybroker", "1883", false},
		{"ssl scheme", "ssl://host:8883", "ssl", "host", "8883", false},
		{"tls scheme", "tls://host:8883", "tls", "host", "8883", false},
		{"mqtts scheme", "mqtts://host:8883", "mqtts", "host", "8883", false},
		{"tcp scheme with bracketed IPv6", "tcp://[2001:db8::1]:1883", "tcp", "2001:db8::1", "1883", false},
		{"ssl scheme with IPv4", "ssl://192.168.1.5:8883", "ssl", "192.168.1.5", "8883", false},
		{"uppercase scheme normalized to lowercase", "MQTTS://host:8883", "mqtts", "host", "8883", false},
		// Scheme-full URLs with a path are parsed via url.Parse (the path must
		// not leak into the host/port).
		{"ws scheme with path", "ws://mybroker/mqtt", "ws", "mybroker", "", false},
		{"wss scheme with port and path", "wss://mybroker:8883/mqtt", "wss", "mybroker", "8883", false},

		// Malformed addresses.
		{"unterminated IPv6 bracket", "[malformed", "", "", "", true},
		{"unterminated IPv6 bracket with scheme", "tcp://[malformed", "", "", "", true},
		{"unterminated IPv6 bracket with port", "[2001:db8::1:1883", "", "", "", true},
		{"stray closing bracket", "]", "", "", "", true},
		{"stray bracket inside brackets", "[]]", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parts, err := parseBroker(tt.broker)
			if tt.wantErr {
				require.Error(t, err, "parseBroker(%q) expected an error", tt.broker)
				return
			}
			require.NoError(t, err, "parseBroker(%q) unexpected error", tt.broker)
			assert.Equal(t, tt.wantScheme, parts.scheme, "parseBroker(%q) scheme mismatch", tt.broker)
			assert.Equal(t, tt.wantHost, parts.host, "parseBroker(%q) host mismatch", tt.broker)
			assert.Equal(t, tt.wantPort, parts.port, "parseBroker(%q) port mismatch", tt.broker)
		})
	}
}

// TestParseBrokerMatchesURLParse is a differential/characterization test: for
// every scheme-full broker address across the dimensions paho accepts (scheme x
// host-type x port x path), parseBroker must extract the same host and port as
// net/url.Parse, the reference the paho client itself uses. This pins the
// engine-swap equivalence that a path-less, hand-picked corpus missed: it fails
// on any parser that lets a URL path or userinfo leak into the host or port.
func TestParseBrokerMatchesURLParse(t *testing.T) {
	t.Parallel()

	schemes := []string{"tcp", "ssl", "tls", "mqtts", "ws", "wss"}
	hosts := []string{"mybroker", "broker.example.com", "192.168.1.5", "[2001:db8::1]", "[::1]"}
	ports := []string{"", ":1883", ":8883"}
	paths := []string{"", "/mqtt", "/path/to/mqtt"}

	for _, sc := range schemes {
		for _, h := range hosts {
			for _, p := range ports {
				for _, pa := range paths {
					addr := sc + "://" + h + p + pa
					t.Run(addr, func(t *testing.T) {
						t.Parallel()
						u, err := url.Parse(addr)
						require.NoError(t, err, "url.Parse(%q)", addr)
						parts, err := parseBroker(addr)
						require.NoError(t, err, "parseBroker(%q)", addr)
						assert.Equal(t, u.Hostname(), parts.host, "host for %q", addr)
						assert.Equal(t, u.Port(), parts.port, "port for %q", addr)
					})
				}
			}
		}
	}
}

// FuzzParseBroker asserts parseBroker never panics on arbitrary input and that
// a successfully-parsed non-empty host always yields a dial-safe host:port.
func FuzzParseBroker(f *testing.F) {
	for _, s := range []string{
		"tcp://h:1883", "mybroker:1883", "[::1]:1883", "ws://h/mqtt",
		"wss://h:8883/mqtt", "", ":1883", "[malformed", "::1", "MQTTS://h:8883",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, broker string) {
		parts, err := parseBroker(broker) // must never panic
		if err != nil {
			return
		}
		if parts.host == "" {
			return
		}
		hp := parts.hostPort()
		if _, _, splitErr := net.SplitHostPort(hp); splitErr != nil {
			t.Errorf("parseBroker(%q).hostPort()=%q is not a valid host:port: %v", broker, hp, splitErr)
		}
	})
}

// TestBrokerHostPort verifies the host:port form used by the connection-test
// TCP/TLS dial stage: IPv6 bracketing via net.JoinHostPort and the default-port
// fallback when the address omitted a port.
func TestBrokerHostPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		broker string
		want   string
	}{
		{"bare hostname gets default port", "mybroker", "mybroker:1883"},
		{"hostname with port preserved", "mybroker:1883", "mybroker:1883"},
		{"scheme stripped, port preserved", "tcp://mybroker:1883", "mybroker:1883"},
		{"bare IPv6 bracketed with default port", "2001:db8::1", "[2001:db8::1]:1883"},
		{"bracketed IPv6 with port preserved", "[::1]:1883", "[::1]:1883"},
		{"bracketed IPv6 without port gets default", "[::1]", "[::1]:1883"},
		{"bare IPv4 gets default port", "192.168.1.5", "192.168.1.5:1883"},
		{"scheme-prefixed host gets default port", "ssl://host", "host:1883"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parts, err := parseBroker(tt.broker)
			require.NoError(t, err, "parseBroker(%q) unexpected error", tt.broker)
			assert.Equal(t, tt.want, parts.hostPort(), "hostPort for %q mismatch", tt.broker)
		})
	}
}

// TestBrokerHostIsIP exercises the parse-then-classify path the connection-test
// flow uses to decide whether to skip the DNS stage (net.ParseIP on the parsed
// host). Malformed inputs are covered as errors by TestParseBroker.
func TestBrokerHostIsIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		broker string
		wantIP bool
	}{
		{"IPv4", "192.168.1.1", true},
		{"IPv4 with scheme and port", "tcp://192.168.1.1:1883", true},
		{"IPv4 loopback", "127.0.0.1", true},
		{"bare IPv6 loopback", "::1", true},
		{"bracketed IPv6", "[::1]", true},
		{"bracketed IPv6 with port", "[::1]:1883", true},
		{"scheme with bracketed IPv6", "tcp://[2001:db8::1]:1883", true},
		{"bare IPv6 global", "2001:db8::1", true},
		{"arbitrary scheme with IPv4", "invalid://192.168.1.1", true},
		{"hostname", "localhost", false},
		{"FQDN with port", "test.mosquitto.org:1883", false},
		{"subdomain with scheme", "mqtt://mqtt.example.com:1883", false},
		{"empty", "", false},
		{"invalid IPv4", "256.256.256.256", false},
		{"invalid IPv6", "2001:zz::1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parts, err := parseBroker(tt.broker)
			require.NoError(t, err, "parseBroker(%q) unexpected error", tt.broker)
			assert.Equal(t, tt.wantIP, net.ParseIP(parts.host) != nil,
				"IP classification for %q (host=%q) mismatch", tt.broker, parts.host)
		})
	}
}
