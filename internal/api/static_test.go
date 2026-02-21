package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/frontend"
)

// TestIsUnhashedAsset verifies the helper correctly identifies unhashed asset paths.
func TestIsUnhashedAsset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "translation JSON file",
			path: "messages/en.json",
			want: true,
		},
		{
			name: "nested translation file",
			path: "messages/pt-BR.json",
			want: true,
		},
		{
			name: "Vite-hashed JS file",
			path: "assets/index-abc123.js",
			want: false,
		},
		{
			name: "Vite-hashed CSS file",
			path: "assets/style-def456.css",
			want: false,
		},
		{
			name: "non-JSON in messages directory",
			path: "messages/README.md",
			want: false,
		},
		{
			name: "JSON file outside messages directory",
			path: "config/settings.json",
			want: false,
		},
		{
			name: "empty path",
			path: "",
			want: false,
		},
		{
			name: "messages directory without trailing file",
			path: "messages/",
			want: false,
		},
		{
			name: "path traversal attempt",
			path: "messages/../secrets/credentials.json",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isUnhashedAsset(tt.path)
			assert.Equal(t, tt.want, got, "isUnhashedAsset(%q)", tt.path)
		})
	}
}

// TestServeFromEmbedCacheHeaders verifies that serveFromEmbed sets appropriate
// Cache-Control headers based on whether assets are hashed or unhashed.
func TestServeFromEmbedCacheHeaders(t *testing.T) {
	// Not parallel: this test mutates the global frontend.DistFS.

	// Save and restore the original DistFS to avoid affecting other tests.
	originalFS := frontend.DistFS
	t.Cleanup(func() {
		frontend.DistFS = originalFS
	})

	// Set up a test filesystem with both hashed and unhashed assets.
	frontend.DistFS = fstest.MapFS{
		"messages/en.json":        &fstest.MapFile{Data: []byte(`{"hello":"Hello"}`)},
		"assets/index-abc123.js":  &fstest.MapFile{Data: []byte(`console.log("app")`)},
		"assets/style-def456.css": &fstest.MapFile{Data: []byte(`body{}`)},
	}

	sfs := NewStaticFileServer()

	tests := []struct {
		name             string
		path             string
		wantCacheControl string
	}{
		{
			name:             "translation JSON gets no-cache",
			path:             "messages/en.json",
			wantCacheControl: "no-cache, must-revalidate",
		},
		{
			name:             "hashed JS gets immutable cache",
			path:             "assets/index-abc123.js",
			wantCacheControl: "public, max-age=31536000, immutable",
		},
		{
			name:             "hashed CSS gets immutable cache",
			path:             "assets/style-def456.css",
			wantCacheControl: "public, max-age=31536000, immutable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/ui/assets/"+tt.path, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := sfs.serveFromEmbed(c, tt.path)
			require.NoError(t, err)

			cacheControl := rec.Header().Get("Cache-Control")
			assert.Equal(t, tt.wantCacheControl, cacheControl,
				"Cache-Control header for path %q", tt.path)
		})
	}
}
