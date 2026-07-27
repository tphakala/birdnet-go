//go:build integration

package containers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPebbleContainer_DirectoryAndRoots is a smoke test: it starts a Pebble
// container and asserts the ACME directory is reachable and the issuing root is
// captured. Constructing the container is the real check (it pulls and boots the
// image and, internally, waits for the ACME directory and fetches the issuing
// root in this environment); the assertions below pin the resulting contract so a
// future change that drops either surfaces here. The full AutoTLS issuance e2e
// lives in internal/api behind the pebble_e2e build tag because it needs the api
// package's unexported server path.
func TestPebbleContainer_DirectoryAndRoots(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)

	httpPort, err := GetFreePort()
	require.NoError(t, err)
	tlsPort, err := GetFreePort()
	require.NoError(t, err)

	pebble, err := NewPebbleContainer(ctx, &PebbleConfig{
		HTTPValidationPort: httpPort,
		TLSValidationPort:  tlsPort,
	})
	require.NoError(t, err, "Pebble container must start with its ACME directory and issuing root reachable")
	t.Cleanup(func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second) //nolint:gocritic // t.Context() is already cancelled in Cleanup
		defer termCancel()
		assert.NoError(t, pebble.Terminate(termCtx))
	})

	assert.Contains(t, pebble.DirectoryURL(), "/dir", "directory URL should point at the ACME directory")
	assert.NotNil(t, pebble.APIHTTPClient(), "API HTTP client should be available")
	assert.NotNil(t, pebble.IssuerRoots(), "issuing root pool should be captured")
}
