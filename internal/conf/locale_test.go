package conf

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverUILocales(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fs       fstest.MapFS
		expected []string
	}{
		{
			name: "discovers locales from json files",
			fs: fstest.MapFS{
				"messages/en.json": &fstest.MapFile{Data: []byte("{}")},
				"messages/de.json": &fstest.MapFile{Data: []byte("{}")},
				"messages/fr.json": &fstest.MapFile{Data: []byte("{}")},
				"messages/sk.json": &fstest.MapFile{Data: []byte("{}")},
			},
			expected: []string{"de", "en", "fr", "sk"},
		},
		{
			name: "adds en when missing",
			fs: fstest.MapFS{
				"messages/de.json": &fstest.MapFile{Data: []byte("{}")},
				"messages/fr.json": &fstest.MapFile{Data: []byte("{}")},
			},
			expected: []string{"de", "en", "fr"},
		},
		{
			name: "ignores non-json files",
			fs: fstest.MapFS{
				"messages/en.json":    &fstest.MapFile{Data: []byte("{}")},
				"messages/README.md":  &fstest.MapFile{Data: []byte("readme")},
				"messages/.gitkeep":   &fstest.MapFile{Data: []byte("")},
				"messages/backup.bak": &fstest.MapFile{Data: []byte("")},
			},
			expected: []string{"en"},
		},
		{
			name: "ignores directories inside messages",
			fs: fstest.MapFS{
				"messages/en.json":          &fstest.MapFile{Data: []byte("{}")},
				"messages/archive/old.json": &fstest.MapFile{Data: []byte("{}")},
			},
			expected: []string{"en"},
		},
		{
			name: "falls back to defaults when messages dir missing",
			fs:   fstest.MapFS{},
			expected: []string{
				"da", "de", "en", "es", "fi", "fr", "hu", "it", "lv", "nl", "pl", "pt", "sk", "sv",
			},
		},
		{
			name: "returns sorted locales",
			fs: fstest.MapFS{
				"messages/sk.json": &fstest.MapFile{Data: []byte("{}")},
				"messages/de.json": &fstest.MapFile{Data: []byte("{}")},
				"messages/en.json": &fstest.MapFile{Data: []byte("{}")},
				"messages/fr.json": &fstest.MapFile{Data: []byte("{}")},
				"messages/it.json": &fstest.MapFile{Data: []byte("{}")},
			},
			expected: []string{"de", "en", "fr", "it", "sk"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := DiscoverUILocales(tt.fs)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSetValidUILocales(t *testing.T) {
	// Save original and restore after test
	original := ValidUILocales()
	t.Cleanup(func() {
		SetValidUILocales(original)
	})

	custom := []string{"en", "fi", "sk"}
	SetValidUILocales(custom)

	got := ValidUILocales()
	require.Equal(t, custom, got)

	// Verify returned slice is a copy (modifying it doesn't affect internal state)
	got[0] = "xx"
	assert.Equal(t, custom, ValidUILocales())
}

func TestValidUILocalesDefault(t *testing.T) {
	// Verify the default includes all current frontend locales.
	// Keep in sync with frontend/static/messages/*.json.
	locales := ValidUILocales()
	expected := []string{"da", "de", "en", "es", "fi", "fr", "hu", "it", "lv", "nl", "pl", "pt", "sk", "sv"}
	for _, code := range expected {
		assert.Contains(t, locales, code, "defaultUILocales is missing %q; update locale.go to match frontend/static/messages/", code)
	}
}

func TestUILocalesDiscovered(t *testing.T) {
	// Save original state and restore after test
	original := ValidUILocales()
	t.Cleanup(func() {
		SetValidUILocales(original)
		// Reset discovered flag by re-setting originals
		validUILocalesMu.Lock()
		uiLocalesDiscovered = false
		validUILocalesMu.Unlock()
	})

	// Before SetValidUILocales is called, discovered should be false
	validUILocalesMu.Lock()
	uiLocalesDiscovered = false
	validUILocalesMu.Unlock()
	assert.False(t, UILocalesDiscovered(), "should be false before SetValidUILocales")

	// After SetValidUILocales, discovered should be true
	SetValidUILocales([]string{"en", "hu"})
	assert.True(t, UILocalesDiscovered(), "should be true after SetValidUILocales")
}
