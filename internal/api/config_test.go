package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestConfigFromSettingsAutoTLSUsesStandardHTTPSPort(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.WebServer.Port = "8080"
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.TLSPort = "9443"

	cfg := ConfigFromSettings(settings)

	require.True(t, cfg.AutoTLS, "expected AutoTLS to be enabled")
	assert.Equal(t, ":8080", cfg.Address(), "regular web address")
	assert.Equal(t, ":443", cfg.TLSAddress(), "AutoTLS HTTPS address")
	assert.Equal(t, ":80", cfg.AutoTLSHTTPAddress(), "AutoTLS HTTP redirect address")
}
