package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseIPWithZone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantNil bool
		wantStr string
	}{
		{"IPv4 unchanged", "192.168.1.1", false, "192.168.1.1"},
		{"IPv6 without zone", "fe80::1", false, "fe80::1"},
		{"IPv6 with zone ID eth0", "fe80::1%eth0", false, "fe80::1"},
		{"IPv6 link-local with zone wlan0", "fe80::1cb6:63bc:5462:71c5%wlan0", false, "fe80::1cb6:63bc:5462:71c5"},
		{"loopback unchanged", "::1", false, "::1"},
		{"empty string returns nil", "", true, ""},
		{"invalid IP with zone returns nil", "not-an-ip%zone", true, ""},
		{"zone ID with numbers", "fe80::1%12", false, "fe80::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseIPWithZone(tt.input)
			if tt.wantNil {
				assert.Nil(t, result, "parseIPWithZone(%q) should return nil", tt.input)
			} else {
				assert.NotNil(t, result, "parseIPWithZone(%q) should not return nil", tt.input)
				assert.Equal(t, tt.wantStr, result.String(), "parseIPWithZone(%q)", tt.input)
			}
		})
	}
}
