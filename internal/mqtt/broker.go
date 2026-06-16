// broker.go: parsing of MQTT broker addresses into scheme/host/port components.
//
// MQTT broker addresses are commonly written without a scheme (for example
// "mybroker:1883"), a form Go's url.Parse mishandles: it rejects scheme-less IP
// literals ("first path segment in URL cannot contain colon") and silently
// reads a scheme-less alphabetic host as the URL scheme. parseBroker is the
// single, IPv6-safe parser that every broker-address consumer in this package
// routes through, so the TLS ServerName, DNS resolution, and the connection
// test stages all interpret the configured broker identically.

package mqtt

import (
	"net"
	"net/url"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// defaultMQTTPort is the port assumed when a broker address omits one.
	defaultMQTTPort = "1883"
	// localhostIP is substituted when a broker address is given as a bare
	// ":port", mirroring paho's ClientOptions.AddBroker.
	localhostIP = "127.0.0.1"
)

// schemeImpliesTLS reports whether a broker URL scheme selects a TLS transport.
// "wss" (WebSocket Secure) is included so a wss broker's TLS config (CA, client
// cert, InsecureSkipVerify) is applied, consistent with the other TLS schemes.
func schemeImpliesTLS(scheme string) bool {
	switch scheme {
	case "ssl", "tls", "mqtts", "wss":
		return true
	default:
		return false
	}
}

// brokerParts holds the components of a parsed MQTT broker address.
type brokerParts struct {
	scheme string // transport scheme without "://" (empty when none was given)
	host   string // hostname or IP literal, without IPv6 brackets
	port   string // port, or "" when the address omitted one
}

// hostPort returns the host:port form, substituting the default MQTT port when
// the address omitted one. IPv6 hosts are bracketed via net.JoinHostPort.
func (b brokerParts) hostPort() string {
	port := b.port
	if port == "" {
		port = defaultMQTTPort
	}
	return net.JoinHostPort(b.host, port)
}

// parseBroker splits an MQTT broker address into its scheme, host, and port.
//
// An address that carries a scheme is a full URL: url.Parse handles it
// correctly, including paths (ws://host/mqtt), userinfo, and IPv6 brackets, and
// matches how the paho client parses the same string. Scheme-less "host:port"
// addresses, which url.Parse mishandles, fall back to splitBrokerHostPort
// (IPv6-safe, accepting both bracketed and bare literals). A genuinely malformed
// address (such as an unterminated IPv6 bracket) returns an error. Schemes are
// normalized to lowercase since they are case-insensitive (RFC 3986).
func parseBroker(broker string) (brokerParts, error) {
	s := strings.TrimSpace(broker)

	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return brokerParts{}, malformedBrokerError(broker, err.Error())
		}
		return brokerParts{scheme: strings.ToLower(u.Scheme), host: u.Hostname(), port: u.Port()}, nil
	}

	host, port, err := splitBrokerHostPort(s)
	if err != nil {
		return brokerParts{}, err
	}
	return brokerParts{scheme: "", host: host, port: port}, nil
}

// splitBrokerHostPort splits a scheme-less "host:port" into host and port. It is
// IPv6-safe, tolerates a missing port, and accepts a bare IPv6 literal. A
// leading ":port" implies the local host, mirroring paho's ClientOptions.AddBroker.
func splitBrokerHostPort(s string) (host, port string, err error) {
	// A leading single ":" (port only, not an IPv6 "::") implies localhost.
	if strings.HasPrefix(s, ":") && !strings.HasPrefix(s, "::") {
		s = localhostIP + s
	}

	if h, p, splitErr := net.SplitHostPort(s); splitErr == nil {
		return h, p, nil
	}

	// net.SplitHostPort failed: either there is no port, or this is a bare IPv6
	// literal. A value opened with "[" must also close with "]".
	if strings.HasPrefix(s, "[") {
		if !strings.HasSuffix(s, "]") {
			return "", "", malformedBrokerError(s, "unterminated IPv6 bracket")
		}
		host = s[1 : len(s)-1]
	} else {
		host = s
	}
	// A returned host must never contain a bracket character. Brackets are only
	// valid as a matched IPv6 delimiter pair (stripped above); a leftover "[" or
	// "]" is malformed and would otherwise yield a non-dial-able host:port.
	if strings.ContainsAny(host, "[]") {
		return "", "", malformedBrokerError(s, "stray bracket in host")
	}
	return host, "", nil
}

// malformedBrokerError builds a consistent configuration error for a broker
// address that cannot be parsed into a usable host.
func malformedBrokerError(addr, reason string) error {
	return errors.Newf("failed to parse broker address %q: %s", addr, reason).
		Component("mqtt").
		Category(errors.CategoryConfiguration).
		Build()
}
