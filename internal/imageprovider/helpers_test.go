package imageprovider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestIsCacheEntryStale tests the TTL logic for cache entries.
func TestIsCacheEntryStale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cachedAt   time.Time
		isNegative bool
		wantStale  bool
	}{
		{
			name:       "fresh positive entry",
			cachedAt:   time.Now().Add(-1 * time.Hour),
			isNegative: false,
			wantStale:  false,
		},
		{
			name:       "stale positive entry (older than 14 days)",
			cachedAt:   time.Now().Add(-15 * 24 * time.Hour),
			isNegative: false,
			wantStale:  true,
		},
		{
			name:       "positive entry just before TTL boundary",
			cachedAt:   time.Now().Add(-14*24*time.Hour + 1*time.Second), // 1 second fresher than TTL
			isNegative: false,
			wantStale:  false,
		},
		{
			name:       "fresh negative entry",
			cachedAt:   time.Now().Add(-5 * time.Minute),
			isNegative: true,
			wantStale:  false,
		},
		{
			name:       "stale negative entry (older than 15 minutes)",
			cachedAt:   time.Now().Add(-20 * time.Minute),
			isNegative: true,
			wantStale:  true,
		},
		{
			name:       "negative entry just before TTL boundary",
			cachedAt:   time.Now().Add(-15*time.Minute + 1*time.Second), // 1 second fresher than TTL
			isNegative: true,
			wantStale:  false,
		},
		{
			name:       "very old positive entry",
			cachedAt:   time.Now().Add(-30 * 24 * time.Hour),
			isNegative: false,
			wantStale:  true,
		},
		{
			name:       "zero time is stale",
			cachedAt:   time.Time{},
			isNegative: false,
			wantStale:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isCacheEntryStale(tt.cachedAt, tt.isNegative)
			assert.Equal(t, tt.wantStale, got, "isCacheEntryStale(%v, %v)", tt.cachedAt, tt.isNegative)
		})
	}
}

// TestDbEntryToBirdImage tests the conversion from datastore.ImageCache to BirdImage.
func TestDbEntryToBirdImage(t *testing.T) {
	t.Parallel()

	cachedTime := time.Now().Add(-1 * time.Hour)

	tests := []struct {
		name  string
		entry *datastore.ImageCache
		want  BirdImage
	}{
		{
			name: "full entry conversion",
			entry: &datastore.ImageCache{
				ScientificName: "Parus major",
				ProviderName:   "wikimedia",
				URL:            "http://example.com/parus.jpg",
				LicenseName:    "CC BY-SA 4.0",
				LicenseURL:     "http://creativecommons.org/licenses/by-sa/4.0/",
				AuthorName:     "John Doe",
				AuthorURL:      "http://example.com/johndoe",
				CachedAt:       cachedTime,
			},
			want: BirdImage{
				ScientificName: "Parus major",
				SourceProvider: "wikimedia",
				URL:            "http://example.com/parus.jpg",
				LicenseName:    "CC BY-SA 4.0",
				LicenseURL:     "http://creativecommons.org/licenses/by-sa/4.0/",
				AuthorName:     "John Doe",
				AuthorURL:      "http://example.com/johndoe",
				CachedAt:       cachedTime,
			},
		},
		{
			name: "negative cache entry",
			entry: &datastore.ImageCache{
				ScientificName: "Unknown species",
				ProviderName:   "wikimedia",
				URL:            negativeEntryMarker,
				CachedAt:       cachedTime,
			},
			want: BirdImage{
				ScientificName: "Unknown species",
				SourceProvider: "wikimedia",
				URL:            negativeEntryMarker,
				CachedAt:       cachedTime,
			},
		},
		{
			name: "minimal entry with empty optional fields",
			entry: &datastore.ImageCache{
				ScientificName: "Turdus merula",
				ProviderName:   "avicommons",
				URL:            "http://example.com/blackbird.jpg",
				CachedAt:       cachedTime,
			},
			want: BirdImage{
				ScientificName: "Turdus merula",
				SourceProvider: "avicommons",
				URL:            "http://example.com/blackbird.jpg",
				CachedAt:       cachedTime,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := dbEntryToBirdImage(tt.entry)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestTruncateResponseBody tests the response body truncation for logging.
func TestTruncateResponseBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		maxLength int
		want      string
	}{
		{
			name:      "body shorter than max length",
			body:      "short",
			maxLength: 10,
			want:      "short",
		},
		{
			name:      "body exactly at max length",
			body:      "1234567890",
			maxLength: 10,
			want:      "1234567890",
		},
		{
			name:      "body longer than max length",
			body:      "this is a very long string that exceeds the limit",
			maxLength: 20,
			want:      "this is a very long ...",
		},
		{
			name:      "empty body",
			body:      "",
			maxLength: 10,
			want:      "",
		},
		{
			name:      "max length of zero",
			body:      "test",
			maxLength: 0,
			want:      "...",
		},
		{
			name:      "max length of one",
			body:      "test",
			maxLength: 1,
			want:      "t...",
		},
		{
			name:      "unicode content truncated by bytes",
			body:      "こんにちは世界", // Each Japanese char is 3 bytes, total 21 bytes
			maxLength: 9,
			want:      "こんに...", // 9 bytes = 3 chars (9 bytes), then "..."
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncateResponseBody(tt.body, tt.maxLength)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestBuildUserAgent tests the user-agent string construction.
func TestBuildUserAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		appVersion string
		wantPrefix string
	}{
		{
			name:       "with version",
			appVersion: "1.2.3",
			wantPrefix: "BirdNETGo/1.2.3",
		},
		{
			name:       "empty version defaults to unknown",
			appVersion: "",
			wantPrefix: "BirdNETGo/unknown",
		},
		{
			name:       "development version",
			appVersion: "dev-build",
			wantPrefix: "BirdNETGo/dev-build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildUserAgent(tt.appVersion)

			// Check it starts with the expected prefix
			assert.Contains(t, got, tt.wantPrefix, "user-agent should contain app name and version")

			// Check it contains required components per Wikimedia policy
			assert.Contains(t, got, userAgentContact, "user-agent should contain contact URL")
			assert.Contains(t, got, userAgentLibrary, "user-agent should contain library name")
			assert.Contains(t, got, "go", "user-agent should contain Go version")
		})
	}
}

// TestCalculateRetryDelay tests the exponential backoff calculation.
func TestCalculateRetryDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		attempt      int
		wantMinDelay time.Duration
	}{
		{
			name:         "attempt 0 returns minimum delay",
			attempt:      0,
			wantMinDelay: retryMinDelay, // 2s
		},
		{
			name:         "attempt 1 returns at least 2 seconds",
			attempt:      1,
			wantMinDelay: 2 * time.Second, // max(2s, 2^1 = 2s)
		},
		{
			name:         "attempt 2 returns at least 4 seconds",
			attempt:      2,
			wantMinDelay: 4 * time.Second, // max(2s, 2^2 = 4s)
		},
		{
			name:         "attempt 3 returns at least 8 seconds",
			attempt:      3,
			wantMinDelay: 8 * time.Second, // max(2s, 2^3 = 8s)
		},
		{
			name:         "attempt 4 returns at least 16 seconds",
			attempt:      4,
			wantMinDelay: 16 * time.Second, // max(2s, 2^4 = 16s)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := calculateRetryDelay(tt.attempt)
			assert.GreaterOrEqual(t, got, tt.wantMinDelay, "delay for attempt %d should be >= %v", tt.attempt, tt.wantMinDelay)
		})
	}

	// Test exponential growth property
	t.Run("delays increase exponentially", func(t *testing.T) {
		t.Parallel()
		prevDelay := calculateRetryDelay(0)
		for attempt := 1; attempt < 5; attempt++ {
			currDelay := calculateRetryDelay(attempt)
			assert.GreaterOrEqual(t, currDelay, prevDelay, "delay should not decrease as attempts increase")
			prevDelay = currDelay
		}
	})
}

// TestBuildDebugURL tests the URL construction for debugging.
func TestBuildDebugURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params map[string]string
		check  func(t *testing.T, result string)
	}{
		{
			name: "basic params",
			params: map[string]string{
				"action": "query",
				"format": "json",
			},
			check: func(t *testing.T, result string) {
				t.Helper()
				assert.Contains(t, result, wikipediaAPIURL, "URL should start with Wikipedia API URL")
				assert.Contains(t, result, "action=query", "URL should contain action param")
				assert.Contains(t, result, "format=json", "URL should contain format param")
			},
		},
		{
			name: "params requiring URL encoding",
			params: map[string]string{
				"titles": "Parus major",
				"action": "query",
			},
			check: func(t *testing.T, result string) {
				t.Helper()
				assert.Contains(t, result, "Parus+major", "space should be URL encoded")
				assert.Contains(t, result, "action=query", "action should be present")
			},
		},
		{
			name:   "empty params",
			params: map[string]string{},
			check: func(t *testing.T, result string) {
				t.Helper()
				assert.Equal(t, wikipediaAPIURL+"?", result, "empty params should just have base URL with ?")
			},
		},
		{
			name: "special characters in params",
			params: map[string]string{
				"titles": "Test&Special=Chars",
			},
			check: func(t *testing.T, result string) {
				t.Helper()
				assert.Contains(t, result, "Test%26Special%3DChars", "special chars should be URL encoded")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildDebugURL(tt.params)
			require.NotEmpty(t, got, "buildDebugURL should not return empty string")
			tt.check(t, got)
		})
	}
}

// TestBirdImageIsNegativeEntry tests the IsNegativeEntry method.
func TestBirdImageIsNegativeEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		img  BirdImage
		want bool
	}{
		{
			name: "positive entry",
			img:  BirdImage{URL: "http://example.com/bird.jpg"},
			want: false,
		},
		{
			name: "negative entry",
			img:  BirdImage{URL: negativeEntryMarker},
			want: true,
		},
		{
			name: "empty URL",
			img:  BirdImage{URL: ""},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.img.IsNegativeEntry()
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestBirdImageGetTTL tests the GetTTL method.
func TestBirdImageGetTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		img  BirdImage
		want time.Duration
	}{
		{
			name: "positive entry gets default TTL",
			img:  BirdImage{URL: "http://example.com/bird.jpg"},
			want: defaultCacheTTL,
		},
		{
			name: "negative entry gets shorter TTL",
			img:  BirdImage{URL: negativeEntryMarker},
			want: negativeCacheTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.img.GetTTL()
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestIsRealError tests the isRealError helper function.
func TestIsRealError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error is not real",
			err:  nil,
			want: false,
		},
		{
			name: "cache miss is not real",
			err:  ErrCacheMiss,
			want: false,
		},
		{
			name: "image not found is real",
			err:  ErrImageNotFound,
			want: true,
		},
		{
			name: "generic error is real",
			err:  assert.AnError,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isRealError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParseAuthorFromHTML tests the author parsing helper function.
func TestParseAuthorFromHTML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		artistHTML     string
		wantAuthorName string
		wantAuthorURL  string
	}{
		{
			name:           "empty string returns Unknown",
			artistHTML:     "",
			wantAuthorName: "Unknown",
			wantAuthorURL:  "",
		},
		{
			name:           "HTML with anchor tag extracts name and URL",
			artistHTML:     `<a href="https://commons.wikimedia.org/wiki/User:JohnDoe">John Doe</a>`,
			wantAuthorName: "John Doe",
			wantAuthorURL:  "https://commons.wikimedia.org/wiki/User:JohnDoe",
		},
		{
			name:           "plain text without link returns text as name",
			artistHTML:     "Some Photographer",
			wantAuthorName: "Some Photographer",
			wantAuthorURL:  "",
		},
		{
			name:           "complex HTML with nested tags",
			artistHTML:     `<a href="http://example.com"><bdi>Artist Name</bdi></a>`,
			wantAuthorName: "Artist Name",
			wantAuthorURL:  "http://example.com",
		},
		{
			name:           "HTML with only text node",
			artistHTML:     `<span>Plain Author</span>`,
			wantAuthorName: "Plain Author",
			wantAuthorURL:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			authorName, authorURL := parseAuthorFromHTML(tt.artistHTML)
			assert.Equal(t, tt.wantAuthorName, authorName, "authorName mismatch")
			assert.Equal(t, tt.wantAuthorURL, authorURL, "authorURL mismatch")
		})
	}
}
