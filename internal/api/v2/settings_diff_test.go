package api

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestDiffSettingsRedactsSecurityAddressLists(t *testing.T) {
	t.Parallel()

	current := &conf.Settings{}
	current.Security.TrustedProxies = []string{"192.0.2.1"}

	updated := &conf.Settings{}
	updated.Security.TrustedProxies = []string{"192.0.2.1", "cloudflare"}
	updated.Security.PlainHTTPSessionClients = []string{"172.18.0.5"}

	changes := diffSettings(current, updated)
	require.NotNil(t, changes)

	redacted := map[string]any{"old": "[redacted]", "new": "[redacted]"}
	assert.Equal(t, redacted, changes["security.trustedproxies.1"])
	assert.Equal(t, redacted, changes["security.plainhttpsessionclients.0"])
	assert.NotContains(t, changes, "security.trustedproxies.0")

	rendered := fmt.Sprint(changes)
	assert.NotContains(t, rendered, "192.0.2.1")
	assert.NotContains(t, rendered, "172.18.0.5")
}
