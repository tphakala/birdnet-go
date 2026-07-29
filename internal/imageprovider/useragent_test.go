package imageprovider

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/useragent"
)

// TestAppUserAgent_DelegatesToSharedBuilder pins the shape this package depends
// on. The memoization behaviour itself — that a version-less string is never
// latched for the process lifetime — is covered where it now lives, in
// internal/useragent.
func TestAppUserAgent_DelegatesToSharedBuilder(t *testing.T) {
	t.Parallel()

	got := appUserAgent()
	assert.Equal(t, useragent.Product(), got, "this package must not build its own header")
	require.Contains(t, got, userAgentName)
	assert.Contains(t, got, userAgentLibrary)
	// The leading token must stay the hyphen-less spelling: the Wikimedia edge
	// refuses a User-Agent that starts with "birdnet-go", case-insensitively.
	assert.Equal(t, userAgentName, strings.Split(got, "/")[0])
	assert.NotContains(t, strings.ToLower(strings.Split(got, "/")[0]), "birdnet-go")
}
