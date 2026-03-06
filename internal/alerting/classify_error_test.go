package alerting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		errMsg  string
		wantKey string
		wantNil bool
	}{
		// Auth errors (use "status NNN" format matching real error strings)
		{name: "status 401", errMsg: "failed with status 401", wantKey: "authError"},
		{name: "status: 403", errMsg: "request failed, status: 403 Forbidden", wantKey: "authError"},
		{name: "unauthorized keyword", errMsg: "server returned unauthorized", wantKey: "authError"},
		{name: "authentication failed", errMsg: "authentication failed for user", wantKey: "authError"},

		// Rate limiting
		{name: "status 429", errMsg: "failed with status 429", wantKey: "rateLimited"},
		{name: "rate limit text", errMsg: "rate limit exceeded", wantKey: "rateLimited"},

		// Gateway timeout (before general timeout)
		{name: "status 504", errMsg: "request failed with status 504", wantKey: "gatewayTimeout"},
		{name: "gateway timeout text", errMsg: "gateway timeout", wantKey: "gatewayTimeout"},

		// Server errors
		{name: "status 500", errMsg: "failed with status 500", wantKey: "serverError"},
		{name: "status 502", errMsg: "upstream returned status 502", wantKey: "serverError"},
		{name: "status: 503", errMsg: "status: 503 Service Unavailable", wantKey: "serverError"},
		{name: "bad gateway text", errMsg: "502 bad gateway", wantKey: "serverError"},

		// Connection refused
		{name: "connection refused", errMsg: "dial tcp 192.168.1.1:443: connection refused", wantKey: "connectionRefused"},

		// DNS errors
		{name: "no such host", errMsg: "dial tcp: lookup api.example.com: no such host", wantKey: "dnsError"},
		{name: "DNS lookup", errMsg: "dns lookup failed", wantKey: "dnsError"},
		{name: "lookup failed", errMsg: "lookup failed for hostname", wantKey: "dnsError"},

		// TLS errors
		{name: "certificate error", errMsg: "x509: certificate signed by unknown authority", wantKey: "tlsError"},
		{name: "TLS handshake", errMsg: "tls: handshake failure", wantKey: "tlsError"},

		// Timeouts
		{name: "timeout", errMsg: "i/o timeout", wantKey: "timeout"},
		{name: "context deadline", errMsg: "context deadline exceeded", wantKey: "timeout"},

		// Connection interrupted
		{name: "EOF standalone", errMsg: "read: eof", wantKey: "connectionInterrupted"},
		{name: "unexpected EOF", errMsg: "unexpected eof from server", wantKey: "connectionInterrupted"},
		{name: "connection reset", errMsg: "read tcp: connection reset by peer", wantKey: "connectionInterrupted"},
		{name: "broken pipe", errMsg: "write: broken pipe", wantKey: "connectionInterrupted"},

		// Disk full
		{name: "no space left", errMsg: "write /data/file: no space left on device", wantKey: "diskFull"},

		// Permission denied
		{name: "permission denied", errMsg: "open /etc/secret: permission denied", wantKey: "permissionDenied"},

		// Unrecognized errors
		{name: "unknown error", errMsg: "something completely unexpected", wantNil: true},
		{name: "empty string", errMsg: "", wantNil: true},

		// Case insensitivity
		{name: "mixed case timeout", errMsg: "Connection Timeout reached", wantKey: "timeout"},
		{name: "uppercase forbidden", errMsg: "FORBIDDEN access to resource", wantKey: "authError"},

		// False-positive regression tests
		{name: "port 5003 not serverError", errMsg: "connection to :5003 failed", wantNil: true},
		{name: "port 4290 not rateLimited", errMsg: "dial tcp :4290 refused", wantNil: true},
		{name: "duration 500ms not serverError", errMsg: "request took 500ms", wantNil: true},
		{name: "name geoff not EOF", errMsg: "user geoff not found", wantNil: true},
		{name: "word dnsmasq not DNS", errMsg: "dnsmasq restarted", wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := classifyError(tt.errMsg)

			if tt.wantNil {
				assert.Nil(t, result, "expected nil classification for %q", tt.errMsg)
				return
			}

			require.NotNil(t, result, "expected classification for %q", tt.errMsg)
			assert.Equal(t, tt.wantKey, result.Key)
			assert.NotEmpty(t, result.Fallback, "fallback message should not be empty")
		})
	}
}

func TestClassifyError_PriorityOrder(t *testing.T) {
	t.Parallel()

	// "status 504 gateway timeout" should match gatewayTimeout, not general timeout
	result := classifyError("request failed with status 504 gateway timeout")
	require.NotNil(t, result)
	assert.Equal(t, "gatewayTimeout", result.Key, "504 should match gatewayTimeout before general timeout")

	// "status 401 unauthorized" should match authError, not serverError
	result = classifyError("failed with status 401 unauthorized")
	require.NotNil(t, result)
	assert.Equal(t, "authError", result.Key)
}
