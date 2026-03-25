package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeNtfyURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bare topic becomes ntfy.sh host",
			input:    "ntfy://mytopic",
			expected: "ntfy://ntfy.sh/mytopic",
		},
		{
			name:     "host and topic unchanged",
			input:    "ntfy://myserver.local/mytopic",
			expected: "ntfy://myserver.local/mytopic",
		},
		{
			name:     "host with port and topic unchanged",
			input:    "ntfy://myserver.local:8080/mytopic",
			expected: "ntfy://myserver.local:8080/mytopic",
		},
		{
			name:     "host with scheme param unchanged",
			input:    "ntfy://myserver.local/mytopic?scheme=http",
			expected: "ntfy://myserver.local/mytopic?scheme=http",
		},
		{
			name:     "bare topic with query becomes ntfy.sh host",
			input:    "ntfy://mytopic?scheme=http",
			expected: "ntfy://ntfy.sh/mytopic?scheme=http",
		},
		{
			name:     "auth with host and topic unchanged",
			input:    "ntfy://user:pass@myserver.local/mytopic?scheme=http",
			expected: "ntfy://user:pass@myserver.local/mytopic?scheme=http",
		},
		{
			name:     "auth with bare topic gets ntfy.sh host",
			input:    "ntfy://user:pass@mytopic",
			expected: "ntfy://user:pass@ntfy.sh/mytopic",
		},
		{
			name:     "username only auth with host unchanged",
			input:    "ntfy://user@myserver.local/mytopic",
			expected: "ntfy://user@myserver.local/mytopic",
		},
		{
			name:     "non-ntfy URL unchanged",
			input:    "discord://token@channelid",
			expected: "discord://token@channelid",
		},
		{
			name:     "empty string unchanged",
			input:    "",
			expected: "",
		},
		{
			name:     "ntfy.sh already present unchanged",
			input:    "ntfy://ntfy.sh/mytopic",
			expected: "ntfy://ntfy.sh/mytopic",
		},
		{
			name:     "IP address host unchanged",
			input:    "ntfy://192.168.1.100:8080/mytopic?scheme=http",
			expected: "ntfy://192.168.1.100:8080/mytopic?scheme=http",
		},
		{
			name:     "localhost host is not modified",
			input:    "ntfy://localhost",
			expected: "ntfy://localhost",
		},
		{
			name:     "localhost host with port is not modified",
			input:    "ntfy://localhost:8080",
			expected: "ntfy://localhost:8080",
		},
		{
			name:     "FQDN host without path is not modified",
			input:    "ntfy://myserver.local",
			expected: "ntfy://myserver.local",
		},
		{
			name:     "custom host with scheme param is not modified",
			input:    "ntfy://myserver.local?scheme=http",
			expected: "ntfy://myserver.local?scheme=http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := NormalizeNtfyURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeNtfyURLs_InProvider(t *testing.T) {
	t.Parallel()

	provider := &PushProviderConfig{
		Type:    "shoutrrr",
		Enabled: true,
		URLs: []string{
			"ntfy://mytopic",
			"ntfy://myserver.local/othertopic?scheme=http",
			"discord://token@channel",
		},
	}

	normalizeNtfyURLs(provider)

	assert.Equal(t, []string{
		"ntfy://ntfy.sh/mytopic",
		"ntfy://myserver.local/othertopic?scheme=http",
		"discord://token@channel",
	}, provider.URLs)
}
