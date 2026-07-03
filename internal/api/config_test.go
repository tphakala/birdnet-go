package api

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestConfigFromSettingsAutoTLSUsesStandardHTTPSPort(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.WebServer.Port = "8080"
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.TLSPort = "9443"

	cfg := ConfigFromSettings(settings)

	if !cfg.AutoTLS {
		t.Fatal("expected AutoTLS to be enabled")
	}
	if got, want := cfg.Address(), ":8080"; got != want {
		t.Fatalf("regular web address = %q, want %q", got, want)
	}
	if got, want := cfg.TLSAddress(), ":443"; got != want {
		t.Fatalf("AutoTLS HTTPS address = %q, want %q", got, want)
	}
	if got, want := cfg.AutoTLSHTTPAddress(), ":80"; got != want {
		t.Fatalf("AutoTLS HTTP redirect address = %q, want %q", got, want)
	}
}
