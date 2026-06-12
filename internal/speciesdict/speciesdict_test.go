package speciesdict

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// expectedLocales mirrors the dashboard UI locale set in frontend/src/lib/i18n/config.ts.
// If the generator's locale list or the UI config drifts from this, the embedded set
// will no longer match and this test fails, pointing at the divergence.
var expectedLocales = []string{
	"cs", "da", "de", "en", "es", "fi", "fr", "hu",
	"it", "lv", "nb", "nl", "pl", "pt", "sk", "sv",
}

func TestSupportedLocales_MatchesUISet(t *testing.T) {
	t.Parallel()
	assert.Equal(t, expectedLocales, SupportedLocales())
}

func TestHas(t *testing.T) {
	t.Parallel()
	assert.True(t, Has("fi"))
	assert.True(t, Has("nb"))
	assert.False(t, Has("xx"))
	assert.False(t, Has(""))
	assert.False(t, Has("../openfauna/data/locales"))
	assert.False(t, Has("en.json.gz")) // the bare locale code is the key, not the filename
}

func TestRead_ReturnsValidGzipJSON(t *testing.T) {
	t.Parallel()

	gz, err := Read("fi")
	require.NoError(t, err)
	require.NotEmpty(t, gz)

	zr, err := gzip.NewReader(bytes.NewReader(gz))
	require.NoError(t, err)
	t.Cleanup(func() { _ = zr.Close() })
	raw, err := io.ReadAll(zr)
	require.NoError(t, err)

	var dict map[string]string
	require.NoError(t, json.Unmarshal(raw, &dict))
	assert.Greater(t, len(dict), 1000)
	assert.Equal(t, "mopsilepakko", dict["Barbastella barbastellus"])
}

func TestRead_UnknownLocale(t *testing.T) {
	t.Parallel()
	_, err := Read("xx")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnknownLocale))
}

func TestVersion_IsStableContentHash(t *testing.T) {
	t.Parallel()
	v := Version()
	assert.NotEmpty(t, v)
	assert.Len(t, v, 12, "version is a truncated content hash")
	assert.Regexp(t, "^[0-9a-f]{12}$", v, "version must be lowercase hex")
	// Deterministic: recomputing over the same embedded content yields the same value.
	assert.Equal(t, v, computeVersion())
	// It is derived from, but not equal to, the bare dataset provenance, so it also
	// changes when only the generator or locale-mapping logic changes the bytes.
	assert.NotEqual(t, openfauna.DataVersion(), v)
}

// Every embedded locale must be a valid, decodable, non-trivial dictionary, so a
// corrupt or empty generated file is caught.
func TestAllEmbeddedDictionariesDecode(t *testing.T) {
	t.Parallel()
	for _, locale := range SupportedLocales() {
		require.True(t, slices.Contains(expectedLocales, locale))
		gz, err := Read(locale)
		require.NoErrorf(t, err, "Read(%q)", locale)

		zr, err := gzip.NewReader(bytes.NewReader(gz))
		require.NoErrorf(t, err, "gzip reader for %q", locale)
		raw, err := io.ReadAll(zr)
		require.NoErrorf(t, err, "decompress %q", locale)
		_ = zr.Close()

		var dict map[string]string
		require.NoErrorf(t, json.Unmarshal(raw, &dict), "unmarshal %q", locale)
		assert.Greaterf(t, len(dict), 1000, "dictionary %q should cover the dataset", locale)
	}
}
