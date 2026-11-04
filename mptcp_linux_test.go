package main

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMultipathTCPDisabled pins the "godebug multipathtcp=0" default in go.mod.
// It lives in package main because that is where the default takes effect.
func TestMultipathTCPDisabled(t *testing.T) {
	// A GODEBUG value in the environment overrides the compiled default, so
	// clear it to test the default itself.
	t.Setenv("GODEBUG", "")

	var lc net.ListenConfig
	assert.False(t, lc.MultipathTCP(), "net listeners must default to plain TCP")

	var d net.Dialer
	assert.False(t, d.MultipathTCP(), "net dialers must default to plain TCP")
}
