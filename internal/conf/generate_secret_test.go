package conf

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRandomSecret(t *testing.T) {
	t.Parallel()

	t.Run("returns non-empty string", func(t *testing.T) {
		t.Parallel()
		secret, err := GenerateRandomSecret()
		require.NoError(t, err)
		assert.NotEmpty(t, secret)
	})

	t.Run("returns 43 character base64url string", func(t *testing.T) {
		t.Parallel()
		secret, err := GenerateRandomSecret()
		require.NoError(t, err)
		// 32 bytes encoded as base64url without padding = 43 characters
		assert.Len(t, secret, 43)
	})

	t.Run("output is valid base64url encoding", func(t *testing.T) {
		t.Parallel()
		secret, err := GenerateRandomSecret()
		require.NoError(t, err)

		decoded, err := base64.RawURLEncoding.DecodeString(secret)
		require.NoError(t, err)
		assert.Len(t, decoded, 32, "decoded secret should be 32 bytes (256 bits)")
	})

	t.Run("generates unique secrets", func(t *testing.T) {
		t.Parallel()
		secret1, err := GenerateRandomSecret()
		require.NoError(t, err)

		secret2, err := GenerateRandomSecret()
		require.NoError(t, err)

		assert.NotEqual(t, secret1, secret2, "two generated secrets should not be identical")
	})
}
