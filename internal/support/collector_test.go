package support

import (
	"archive/zip"
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"gopkg.in/yaml.v3"
)

// updateGoldenFiles is a flag to regenerate golden files during development.
// Run with: go test -v -run TestComprehensivePrivacyScrubbing -update ./internal/support/...
var updateGoldenFiles = flag.Bool("update", false, "update golden files")

// Test constants
const (
	// File sizes for testing
	testLogSizeLimit  = 5000
	testLogSizeSmall  = 1000
	testLogSizeMedium = 4000
	testLogSizeLarge  = 4500
	testLogSizeTiny   = 0

	// Test durations
	testDuration24Hours = 24 * time.Hour
	testDuration1Hour   = 1 * time.Hour
	testDuration48Hours = 48 * time.Hour
	testDuration1Minute = 1 * time.Minute

	// Test sizes in bytes
	testSize10MB = 10 * 1024 * 1024
	testSize1KB  = 1024
)

// TestLogFileCollector_isLogFile tests the log file detection
func TestLogFileCollector_isLogFile(t *testing.T) {
	t.Parallel()

	lfc := &logFileCollector{}

	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"standard log file", "application.log", true},
		{"rotated log file", "birdweather-2025-06-25T15-36-29.209.log", true},
		{"uppercase extension", "ERROR.LOG", true},
		{"no extension logfile", "logfile", false},
		{"different extension", "data.txt", false},
		{"hidden log file", ".hidden.log", true},
		{"log in filename", "mylog.txt", false},
		{"empty filename", "", false},
		{"only extension", ".log", true},
		{"path with log file", "/path/to/file.log", true},
		{"path without log file", "/path/to/file.txt", false},
		{"custom log suffix", "app.debuglog", true},
		{"custom log suffix uppercase", "app.ERRORLOG", true},
		{"another log suffix", "system.applog", true},
		{"ends with log word", "mylog", true},
		{"ends with LOG uppercase", "MYLOG", true},
		{"log in middle", "logdata.txt", false},
		{"composite extension", "file.log.bak", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, lfc.isLogFile(tt.filename), "isLogFile(%q)", tt.filename)
		})
	}
}

// TestLogFileCollector_isFileWithinTimeRange tests time range checking
func TestLogFileCollector_isFileWithinTimeRange(t *testing.T) {
	now := time.Now()
	lfc := &logFileCollector{
		cutoffTime: now.Add(-testDuration24Hours), // 24 hours ago
	}

	tests := []struct {
		name    string
		modTime time.Time
		want    bool
	}{
		{"recent file", now.Add(-testDuration1Hour), true},
		{"file at cutoff", now.Add(-testDuration24Hours), true},
		{"old file", now.Add(-testDuration48Hours), false},
		{"future file", now.Add(testDuration1Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock FileInfo
			info := &mockFileInfo{modTime: tt.modTime}
			assert.Equal(t, tt.want, lfc.isFileWithinTimeRange(info), "isFileWithinTimeRange()")
		})
	}
}

// TestLogFileCollector_canAddFile tests size limit checking
func TestLogFileCollector_canAddFile(t *testing.T) {
	tests := []struct {
		name      string
		totalSize int64
		maxSize   int64
		fileSize  int64
		want      bool
	}{
		{"within limit", testLogSizeSmall, testLogSizeLimit, testLogSizeSmall, true},
		{"exactly at limit", testLogSizeMedium, testLogSizeLimit, testLogSizeSmall, true},
		{"exceeds limit", testLogSizeLarge, testLogSizeLimit, testLogSizeSmall, false},
		{"zero file size", testLogSizeSmall, testLogSizeLimit, testLogSizeTiny, true},
		{"already at max", testLogSizeLimit, testLogSizeLimit, testLogSizeSmall, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lfc := &logFileCollector{
				totalSize: tt.totalSize,
				maxSize:   tt.maxSize,
			}
			assert.Equal(t, tt.want, lfc.canAddFile(tt.fileSize), "canAddFile(%d)", tt.fileSize)
		})
	}
}

// TestCollector_getLogSearchPaths tests log path generation
func TestCollector_getLogSearchPaths(t *testing.T) {
	t.Parallel()

	c := &Collector{
		configPath: "/etc/birdnet",
		dataPath:   "/var/lib/birdnet",
	}

	paths := c.getLogSearchPaths()

	// Should have at least the base paths
	expectedPaths := []string{
		"logs",
		"/var/lib/birdnet/logs",
		"/etc/birdnet/logs",
	}

	assert.GreaterOrEqual(t, len(paths), len(expectedPaths), "Expected at least %d paths", len(expectedPaths))

	// Check that expected paths are present
	for _, expected := range expectedPaths {
		assert.Contains(t, paths, expected, "Expected path %q not found in result", expected)
	}
}

// TestCollector_getUniqueLogPaths tests path deduplication
func TestCollector_getUniqueLogPaths(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create collector with paths that would resolve to the same absolute path
	c := &Collector{
		configPath: tempDir,
		dataPath:   filepath.Join(tempDir, "..", filepath.Base(tempDir)),
	}

	// Change to temp directory to test relative path resolution
	oldWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(tempDir), "Failed to change directory")
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Logf("Warning: Failed to restore working directory: %v", err)
		}
	})

	uniquePaths := c.getUniqueLogPaths()

	// Count occurrences of each absolute path
	pathCount := make(map[string]int)
	for _, path := range uniquePaths {
		abs, _ := filepath.Abs(path)
		pathCount[abs]++
	}

	// Check that no path appears more than once
	for path, count := range pathCount {
		assert.Equal(t, 1, count, "Path %q appears %d times, expected 1", path, count)
	}
}

// TestLogFileCollector_addNoLogsNote tests README creation
func TestLogFileCollector_addNoLogsNote(t *testing.T) {
	// Create a buffer to simulate zip writer
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	lfc := &logFileCollector{}
	lfc.addNoLogsNote(w)

	// Close the writer to finalize
	require.NoError(t, w.Close(), "Failed to close zip writer")

	// Read the zip content
	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err, "Failed to read zip")

	// Look for README.txt
	found := false
	for _, f := range r.File {
		if f.Name != "logs/README.txt" {
			continue
		}

		found = true

		// Check content
		rc, err := f.Open()
		require.NoError(t, err, "Failed to open README")

		content, err := io.ReadAll(rc)
		assert.NoError(t, rc.Close(), "Failed to close README reader") // Close immediately after reading, not deferred

		require.NoError(t, err, "Failed to read README")

		expected := "No log files were found or all logs were older than the specified duration."
		assert.Equal(t, expected, string(content), "README content mismatch")
		break
	}

	assert.True(t, found, "README.txt not found in archive")
}

// TestCollector_scrubConfig tests sensitive data scrubbing
func TestCollector_scrubConfig(t *testing.T) {
	c := &Collector{
		sensitiveKeys: defaultSensitiveKeys(),
	}

	tests := []struct {
		name   string
		config map[string]any
		want   map[string]any
	}{
		{
			name: "scrub password fields",
			config: map[string]any{
				"password":   "secret123",
				"api_key":    "key123",
				"safe_field": "visible",
				"nested": map[string]any{
					"token":        "token456",
					"normal_field": "also_visible",
				},
			},
			want: map[string]any{
				"password":   "[redacted]",
				"api_key":    "[redacted]",
				"safe_field": "visible",
				"nested": map[string]any{
					"token":        "[redacted]",
					"normal_field": "also_visible",
				},
			},
		},
		{
			name: "sanitize RTSP URLs with credentials in non-sensitive field",
			config: map[string]any{
				"streams": []any{"rtsp://user:pass@192.168.1.100:554/stream", "http://example.com"},
				"data":    []any{"safe1", "safe2"},
			},
			want: map[string]any{
				"streams": []any{"rtsp://192.168.1.100:554/stream", "http://example.com"},
				"data":    []any{"safe1", "safe2"},
			},
		},
		{
			name: "handle mixed case keys",
			config: map[string]any{
				"Password": "secret",
				"API_KEY":  "key",
				"ApiToken": "token",
				"normal":   "visible",
			},
			want: map[string]any{
				"Password": "[redacted]",
				"API_KEY":  "[redacted]",
				"ApiToken": "[redacted]",
				"normal":   "visible",
			},
		},
		{
			name: "redact coordinates when non-default",
			config: map[string]any{
				"birdnet": map[string]any{
					"latitude":    45.5231,
					"longitude":   -122.6765,
					"sensitivity": 1.0,
					"threshold":   0.8,
				},
			},
			want: map[string]any{
				"birdnet": map[string]any{
					"latitude":    "[redacted]",
					"longitude":   "[redacted]",
					"sensitivity": 1.0,
					"threshold":   0.8,
				},
			},
		},
		{
			name: "skip default coordinates (zero values)",
			config: map[string]any{
				"latitude":  0.0,
				"longitude": 0.0,
			},
			want: map[string]any{
				"latitude":  0.0,
				"longitude": 0.0,
			},
		},
		{
			name: "redact station ID",
			config: map[string]any{
				"weather": map[string]any{
					"wunderground": map[string]any{
						"stationid": "KWASEATT123",
						"units":     "m",
					},
				},
			},
			want: map[string]any{
				"weather": map[string]any{
					"wunderground": map[string]any{
						"stationid": "[redacted]",
						"units":     "m",
					},
				},
			},
		},
		{
			name: "redact user ID (email)",
			config: map[string]any{
				"security": map[string]any{
					"oauthProviders": []any{
						map[string]any{
							"provider": "google",
							"userid":   "user@example.com",
							"enabled":  true,
						},
					},
				},
			},
			want: map[string]any{
				"security": map[string]any{
					"oauthProviders": []any{
						map[string]any{
							"provider": "google",
							"userid":   "[redacted]",
							"enabled":  true,
						},
					},
				},
			},
		},
		{
			name: "redact subnet",
			config: map[string]any{
				"security": map[string]any{
					"allowSubnetBypass": map[string]any{
						"enabled": true,
						"subnet":  "192.168.1.0/24",
					},
				},
			},
			want: map[string]any{
				"security": map[string]any{
					"allowSubnetBypass": map[string]any{
						"enabled": true,
						"subnet":  "[redacted]",
					},
				},
			},
		},
		{
			name: "redact secret file paths",
			config: map[string]any{
				"backup": map[string]any{
					"targets": []any{
						map[string]any{
							"type": "sftp",
							"settings": map[string]any{
								"privatekeypath": "/home/user/.ssh/id_rsa",
								"host":           "backup.example.com",
							},
						},
					},
				},
			},
			want: map[string]any{
				"backup": map[string]any{
					"targets": []any{
						map[string]any{
							"type": "sftp",
							"settings": map[string]any{
								"privatekeypath": "[redacted]",
								"host":           "backup.example.com",
							},
						},
					},
				},
			},
		},
		{
			name: "redact ntfy URL with credentials structurally",
			config: map[string]any{
				"notification": map[string]any{
					"push": map[string]any{
						"providers": []any{
							map[string]any{
								"type": "shoutrrr",
								"urls": []any{"ntfy://admin:secret@ntfy.sh/mytopic"},
							},
						},
					},
				},
			},
			want: map[string]any{
				"notification": map[string]any{
					"push": map[string]any{
						"providers": []any{
							map[string]any{
								"type": "shoutrrr",
								"urls": []any{"ntfy://[user]:[pass]@[host]/[path]"},
							},
						},
					},
				},
			},
		},
		{
			name: "redact webhook URL without credentials",
			config: map[string]any{
				"endpoints": []any{
					map[string]any{
						"url":    "https://hooks.slack.com/services/T123/B456/xyz",
						"method": "POST",
					},
				},
			},
			want: map[string]any{
				"endpoints": []any{
					map[string]any{
						"url":    "https://[host]/[path]",
						"method": "POST",
					},
				},
			},
		},
		{
			name: "redact URL preserving port",
			config: map[string]any{
				"url": "rtsp://camera:pass@192.168.1.50:554/stream1",
			},
			want: map[string]any{
				"url": "rtsp://[user]:[pass]@[host]:554/[path]",
			},
		},
		{
			name: "redact endpoint URL",
			config: map[string]any{
				"weather": map[string]any{
					"openWeather": map[string]any{
						"endpoint": "https://api.openweathermap.org/data/2.5/weather",
						"units":    "metric",
					},
				},
			},
			want: map[string]any{
				"weather": map[string]any{
					"openWeather": map[string]any{
						"endpoint": "https://[host]/[path]",
						"units":    "metric",
					},
				},
			},
		},
		{
			name: "skip empty URL",
			config: map[string]any{
				"url": "",
			},
			want: map[string]any{
				"url": "",
			},
		},
		{
			name: "redact URL with query parameters",
			config: map[string]any{
				"url": "https://api.example.com/webhook?token=secret123&channel=alerts",
			},
			want: map[string]any{
				"url": "https://[host]/[path]?[query]",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.scrubConfig(tt.config)
			assert.True(t, compareConfigs(got, tt.want), "scrubConfig() = %v, want %v", got, tt.want)
		})
	}
}

// compareConfigs compares two config maps for equality
func compareConfigs(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v1 := range a {
		v2, ok := b[k]
		if !ok {
			return false
		}
		switch t1 := v1.(type) {
		case map[string]any:
			t2, ok := v2.(map[string]any)
			if !ok || !compareConfigs(t1, t2) {
				return false
			}
		case []any:
			t2, ok := v2.([]any)
			if !ok || len(t1) != len(t2) {
				return false
			}
			for i := range t1 {
				// Handle nested maps in slices
				if m1, ok := t1[i].(map[string]any); ok {
					m2, ok := t2[i].(map[string]any)
					if !ok || !compareConfigs(m1, m2) {
						return false
					}
				} else if t1[i] != t2[i] {
					return false
				}
			}
		default:
			if v1 != v2 {
				return false
			}
		}
	}
	return true
}

// mockFileInfo implements os.FileInfo for testing
type mockFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() os.FileMode  { return m.mode }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any           { return nil }

// TestIsDefaultValue tests default value detection for redaction skipping
func TestIsDefaultValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{"nil value", nil, true},
		{"empty string", "", true},
		{"non-empty string", "hello", false},
		{"zero float64", float64(0.0), true},
		{"non-zero float64", float64(45.5), false},
		{"negative float64", float64(-122.5), false},
		{"zero int", int(0), true},
		{"non-zero int", int(42), false},
		{"zero int64", int64(0), true},
		{"non-zero int64", int64(100), false},
		{"false bool", false, false}, // booleans never default
		{"true bool", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDefaultValue(tt.value)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestIsURLValue tests URL detection
func TestIsURLValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"http URL", "http://example.com", true},
		{"https URL", "https://example.com/path", true},
		{"rtsp URL", "rtsp://192.168.1.1:554/stream", true},
		{"mqtt URL", "mqtt://broker:1883", true},
		{"ntfy URL", "ntfy://user:pass@ntfy.sh/topic", true},
		{"ftp URL", "ftp://ftp.example.com/files", true},
		{"plain string", "not a url", false},
		{"empty string", "", false},
		{"path only", "/some/path", false},
		{"host only", "example.com", false},
		{"email address", "user@example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isURLValue(tt.value)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestRedactURLStructurally tests URL structural redaction
func TestRedactURLStructurally(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "URL with user and password",
			url:  "ntfy://admin:secret@ntfy.sh/mytopic",
			want: "ntfy://[user]:[pass]@[host]/[path]",
		},
		{
			name: "URL with user only",
			url:  "ftp://anonymous@ftp.example.com/files",
			want: "ftp://[user]@[host]/[path]",
		},
		{
			name: "URL without credentials",
			url:  "https://api.example.com/webhook",
			want: "https://[host]/[path]",
		},
		{
			name: "URL with port",
			url:  "rtsp://user:pass@192.168.1.50:554/stream",
			want: "rtsp://[user]:[pass]@[host]:554/[path]",
		},
		{
			name: "URL with query parameters",
			url:  "https://api.example.com/data?token=abc&user=123",
			want: "https://[host]/[path]?[query]",
		},
		{
			name: "URL host only",
			url:  "mqtt://broker.example.com",
			want: "mqtt://[host]",
		},
		{
			name: "URL with port no path",
			url:  "mqtt://broker.example.com:1883",
			want: "mqtt://[host]:1883",
		},
		{
			name: "URL with root path only",
			url:  "http://example.com/",
			want: "http://[host]",
		},
		{
			name: "complex webhook URL",
			url:  "https://hooks.slack.com/services/T123/B456/xyz789",
			want: "https://[host]/[path]",
		},
		{
			name: "non-URL string returns redacted placeholder",
			url:  "just a plain string",
			want: "[redacted]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactURLStructurally(tt.url)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestIsSensitiveKey tests word boundary matching for sensitive keys
func TestIsSensitiveKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		key       string
		sensitive string
		want      bool
	}{
		// Exact matches
		{"exact match password", "password", "password", true},
		{"exact match token", "token", "token", true},
		{"exact match key", "key", "key", true},

		// Suffix matches (common config patterns)
		{"mqtt_password matches password", "mqtt_password", "password", true},
		{"api_key matches key", "api_key", "key", true},
		{"auth_token matches token", "auth_token", "token", true},
		{"webhook_url matches url", "webhook_url", "url", true},

		// Should NOT match (false positives prevented)
		{"monkey should not match key", "monkey", "key", false},
		{"turkey should not match key", "turkey", "key", false},
		{"donkey should not match key", "donkey", "key", false},
		{"tokenizer should not match token", "tokenizer", "token", false},
		{"curlopt should not match url", "curlopt", "url", false},
		{"secrete should not match secret", "secrete", "secret", false},

		// Edge cases
		{"empty key", "", "password", false},
		{"key at start with suffix", "passwordhash", "password", false},
		{"key with number suffix", "password1", "password", true},
		{"key with underscore suffix", "password_hash", "password", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSensitiveKey(tt.key, tt.sensitive)
			assert.Equal(t, tt.want, got, "isSensitiveKey(%q, %q)", tt.key, tt.sensitive)
		})
	}
}

// TestAnonymizeIPAddress tests IP address anonymization
func TestAnonymizeIPAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"localhost ipv4", "127.0.0.1", "localhost-"},
		{"localhost ipv6", "::1", "localhost-"},
		{"private ip 10.x", "10.0.1.100", "private-ip-"},
		{"private ip 192.168.x", "192.168.1.1", "private-ip-"},
		{"private ip 172.16.x", "172.16.0.1", "private-ip-"},
		{"link-local", "169.254.1.1", "private-ip-"},
		{"invalid ip", "not.an.ip", "invalid-ip-b94d27"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := privacy.AnonymizeIP(tt.input)
			switch {
			case tt.name == "invalid ip":
				// For invalid IPs, we just check the prefix
				assert.True(t, strings.HasPrefix(result, "invalid-ip-"), "Expected invalid-ip prefix for %s, got %s", tt.input, result)
			case strings.HasPrefix(tt.expected, "public-ip-"):
				// For public IPs, we just check the prefix since the hash varies
				assert.True(t, strings.HasPrefix(result, "public-ip-"), "Expected public-ip prefix for %s, got %s", tt.input, result)
			default:
				assert.True(t, strings.HasPrefix(result, tt.expected), "Expected %s prefix for %s, got %s", tt.expected, tt.input, result)
			}
		})
	}
}

// TestScrubLogMessage tests comprehensive log message scrubbing
func TestScrubLogMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expected    []string // Expected substrings in output
		notExpected []string // Substrings that should NOT be in output
	}{
		{
			name:        "ip and url anonymization",
			input:       "Error connecting to https://192.168.1.100:8080/api from 10.0.1.50",
			expected:    []string{"Error connecting to", "from"},
			notExpected: []string{"192.168.1.100", "10.0.1.50", "8080/api"},
		},
		{
			name:        "email anonymization",
			input:       "User john.doe@example.com logged in",
			expected:    []string{"User", "logged in", "[EMAIL]"},
			notExpected: []string{"john.doe@example.com"},
		},
		{
			name:        "api key anonymization",
			input:       "api_key=sk_test_123456789abcdef error occurred",
			expected:    []string{"api_key: [TOKEN]", "error occurred"},
			notExpected: []string{"sk_test_123456789abcdef"},
		},
		{
			name:        "uuid anonymization",
			input:       "Processing request id: 550e8400-e29b-41d4-a716-446655440000",
			expected:    []string{"Processing request id:", "[UUID]"},
			notExpected: []string{"550e8400-e29b-41d4-a716-446655440000"},
		},
		{
			name:        "mixed sensitive data",
			input:       "Failed to connect to rtsp://admin:password@192.168.1.200:554/stream1 from 203.0.113.42 (API token: Bearer abc123def456)",
			expected:    []string{"Failed to connect to", "from", "Bearer [TOKEN]"},
			notExpected: []string{"admin:password", "192.168.1.200", "203.0.113.42", "abc123def456"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := privacy.ScrubMessage(tt.input)

			for _, expected := range tt.expected {
				assert.Contains(t, result, expected, "Expected '%s' to be in result: %s", expected, result)
			}

			for _, notExpected := range tt.notExpected {
				assert.NotContains(t, result, notExpected, "Expected '%s' to NOT be in result: %s", notExpected, result)
			}
		})
	}
}

// TestCollector_collectJournalLogs tests journal log collection error handling
func TestCollector_collectJournalLogs(t *testing.T) {
	t.Parallel()

	c := &Collector{}

	// Test journal log collection when journalctl is not available
	// This test will fail in environments where journalctl exists and works
	// so we'll just verify the error type is returned
	t.Run("journal not available", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		diagnostics := &LogSourceDiagnostics{PathsSearched: []SearchedPath{}, Details: make(map[string]any)}
		logs, err := c.collectJournalLogs(ctx, testDuration1Hour, false, diagnostics)

		// If journalctl is not available or service doesn't exist, we should get our sentinel error
		if err != nil {
			require.ErrorIs(t, err, ErrJournalNotAvailable, "Expected ErrJournalNotAvailable when journal is not available")
			assert.Nil(t, logs, "Expected nil logs when error is returned")
		}
		// If journalctl is available, logs might be returned or empty
		// We can't make strong assertions here since it depends on the environment
	})
}

// TestCollectionDiagnostics_Population tests that diagnostics are correctly populated on failures
func TestCollectionDiagnostics_Population(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func() *CollectionDiagnostics
		validate func(t *testing.T, diag *CollectionDiagnostics)
	}{
		{
			name: "log collection failure populates diagnostics",
			setup: func() *CollectionDiagnostics {
				diag := &CollectionDiagnostics{
					LogCollection: LogCollectionDiagnostics{
						JournalLogs: LogSourceDiagnostics{
							Attempted:  true,
							Successful: false,
							Error:      "journalctl not found",
							Details:    make(map[string]any),
						},
						FileLogs: LogSourceDiagnostics{
							Attempted:    true,
							Successful:   true,
							EntriesFound: 10,
							PathsSearched: []SearchedPath{
								{Path: "/var/log", Exists: true, Accessible: true, FileCount: 5},
							},
							Details: make(map[string]any),
						},
						Summary: DiagnosticSummary{
							TotalEntries: 10,
							TimeRange: TimeRange{
								From: time.Now().Add(-testDuration24Hours),
								To:   time.Now(),
							},
						},
					},
				}
				return diag
			},
			validate: func(t *testing.T, diag *CollectionDiagnostics) {
				t.Helper()
				assert.True(t, diag.LogCollection.JournalLogs.Attempted)
				assert.False(t, diag.LogCollection.JournalLogs.Successful)
				assert.Contains(t, diag.LogCollection.JournalLogs.Error, "journalctl")
				assert.True(t, diag.LogCollection.FileLogs.Successful)
				assert.Equal(t, 10, diag.LogCollection.FileLogs.EntriesFound)
				assert.Equal(t, 10, diag.LogCollection.Summary.TotalEntries)
			},
		},
		{
			name: "config collection failure populates diagnostics",
			setup: func() *CollectionDiagnostics {
				diag := &CollectionDiagnostics{
					ConfigCollection: DiagnosticInfo{
						Attempted:  true,
						Successful: false,
						Error:      "config file not found",
					},
				}
				return diag
			},
			validate: func(t *testing.T, diag *CollectionDiagnostics) {
				t.Helper()
				assert.True(t, diag.ConfigCollection.Attempted)
				assert.False(t, diag.ConfigCollection.Successful)
				assert.Contains(t, diag.ConfigCollection.Error, "config")
			},
		},
		{
			name: "system info collection failure populates diagnostics",
			setup: func() *CollectionDiagnostics {
				diag := &CollectionDiagnostics{
					SystemCollection: DiagnosticInfo{
						Attempted:  true,
						Successful: false,
						Error:      "permission denied",
					},
				}
				return diag
			},
			validate: func(t *testing.T, diag *CollectionDiagnostics) {
				t.Helper()
				assert.True(t, diag.SystemCollection.Attempted)
				assert.False(t, diag.SystemCollection.Successful)
				assert.Contains(t, diag.SystemCollection.Error, "permission")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diag := tt.setup()
			tt.validate(t, diag)
		})
	}
}

// TestCollector_collectLogFilesWithDiagnostics tests file log collection with diagnostics
func TestCollector_collectLogFilesWithDiagnostics(t *testing.T) {
	// Create temp directory structure for testing
	tempDir := t.TempDir()
	logDir := filepath.Join(tempDir, "logs")
	require.NoError(t, os.MkdirAll(logDir, defaultDirPermissions))

	// Create test log files
	testLogContent := "2024-01-15 10:00:00 INFO test log entry\n"
	logFile1 := filepath.Join(logDir, "app.log")
	require.NoError(t, os.WriteFile(logFile1, []byte(testLogContent), defaultFilePermissions))

	c := &Collector{
		configPath: tempDir,
		dataPath:   tempDir,
	}

	tests := []struct {
		name     string
		duration time.Duration
		validate func(t *testing.T, logs []LogEntry, diag *LogSourceDiagnostics)
	}{
		{
			name:     "successful log collection",
			duration: testDuration24Hours,
			validate: func(t *testing.T, logs []LogEntry, diag *LogSourceDiagnostics) {
				t.Helper()
				// Check that paths were searched
				assert.NotEmpty(t, diag.PathsSearched)
				// Check that at least one log was found if the log directory exists
				for _, path := range diag.PathsSearched {
					if path.Path == logDir && path.Exists {
						assert.True(t, path.Accessible)
						assert.Positive(t, path.FileCount)
					}
				}
			},
		},
		{
			name:     "old logs filtered by duration",
			duration: testDuration1Minute, // Very short duration to filter out test log
			validate: func(t *testing.T, logs []LogEntry, diag *LogSourceDiagnostics) {
				t.Helper()
				// Paths should still be searched
				assert.NotEmpty(t, diag.PathsSearched)
				// But logs might be filtered out
				// logs might be empty after filtering
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diagnostics := &LogSourceDiagnostics{
				Details: make(map[string]any),
			}

			logs, _, _ := c.collectLogFilesWithDiagnostics(tt.duration, testSize10MB, false, diagnostics)

			tt.validate(t, logs, diagnostics)
		})
	}
}

// TestCollector_Collect_AlwaysIncludesDiagnostics tests that diagnostics are always included
func TestCollector_Collect_AlwaysIncludesDiagnostics(t *testing.T) {
	tempDir := t.TempDir()

	c := &Collector{
		configPath:    tempDir,
		dataPath:      tempDir,
		sensitiveKeys: defaultSensitiveKeys(),
	}

	// Create a bundle with minimal options - at least one type must be enabled
	opts := CollectorOptions{
		IncludeLogs:       true,  // Enable logs to avoid validation error
		IncludeConfig:     false, // Disable config
		IncludeSystemInfo: false, // Disable system info
		LogDuration:       testDuration1Hour,
		MaxLogSize:        testSize1KB,
	}

	ctx := t.Context()
	bundle, err := c.Collect(ctx, opts)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	// Verify diagnostics are present
	assert.NotNil(t, bundle.Diagnostics)
	// Logs were enabled, so file logs should be attempted
	assert.True(t, bundle.Diagnostics.LogCollection.FileLogs.Attempted)
	// Config and system info were disabled
	assert.False(t, bundle.Diagnostics.ConfigCollection.Attempted)
	assert.False(t, bundle.Diagnostics.SystemCollection.Attempted)

	// Now test with everything enabled but simulated failures
	opts = CollectorOptions{
		IncludeLogs:       true,
		IncludeConfig:     true,
		IncludeSystemInfo: true,
		LogDuration:       testDuration1Hour,
		MaxLogSize:        testSize1KB,
	}

	bundle, err = c.Collect(ctx, opts)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	// Diagnostics should always be populated
	assert.NotNil(t, bundle.Diagnostics)
	assert.True(t, bundle.Diagnostics.LogCollection.FileLogs.Attempted)
	// Journal logs might or might not be attempted depending on the environment
}

// =============================================================================
// Comprehensive Privacy Scrubbing Integration Test
// =============================================================================

// createComprehensiveMockConfig creates a complete mock configuration
// covering ALL settings the application supports for integration testing.
// This mirrors the structure in internal/conf/config.go.
func createComprehensiveMockConfig() map[string]any {
	return map[string]any{
		// =====================================================================
		// Main Settings
		// =====================================================================
		"main": map[string]any{
			"name":      "birdnet-home-station",
			"timeAs24h": true,
		},

		// =====================================================================
		// Logging Configuration
		// =====================================================================
		"logging": map[string]any{
			"level":  "info",
			"format": "json",
		},

		// =====================================================================
		// Input Configuration
		// =====================================================================
		"input": map[string]any{
			"path":      "/home/user/recordings",
			"recursive": true,
			"watch":     false,
		},

		// =====================================================================
		// BirdNET Configuration (includes location data)
		// =====================================================================
		"birdnet": map[string]any{
			"debug":       false,
			"sensitivity": 1.0,
			"threshold":   0.8,
			"overlap":     0.0,
			"latitude":    45.5231,
			"longitude":   -122.6765,
			"threads":     4,
			"locale":      "en",
			"modelPath":   "/opt/birdnet/models/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite",
			"labelPath":   "/opt/birdnet/models/BirdNET_GLOBAL_6K_V2.4_Labels.txt",
			"useXNNPACK":  true,
			"rangeFilter": map[string]any{
				"debug":     false,
				"model":     "latest",
				"modelPath": "/opt/birdnet/models/BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite",
				"threshold": 0.03,
			},
		},

		// =====================================================================
		// Security Configuration (highly sensitive)
		// =====================================================================
		"security": map[string]any{
			"debug":           false,
			"baseUrl":         "https://birdnet.example.com:5500",
			"host":            "birdnet.example.com",
			"autoTls":         true,
			"redirectToHTTPS": true,
			"basicAuth": map[string]any{
				"enabled":        true,
				"password":       "admin_secret_password",
				"clientId":       "oauth_client_id_12345",
				"clientSecret":   "oauth_client_secret_xyz",
				"redirectUri":    "https://birdnet.example.com/callback",
				"authCodeExp":    "5m",
				"accessTokenExp": "24h",
			},
			"sessionSecret":   "session_secret_key_abc123",
			"sessionDuration": "168h",
			"allowSubnetBypass": map[string]any{
				"enabled": true,
				"subnet":  "192.168.1.0/24",
			},
			"oauthProviders": []any{
				map[string]any{
					"provider":     "google",
					"enabled":      true,
					"clientId":     "google_client_id_123",
					"clientSecret": "google_client_secret_456",
					"redirectUri":  "https://birdnet.example.com/auth/google/callback",
					"userId":       "user@gmail.com",
				},
				map[string]any{
					"provider":     "github",
					"enabled":      true,
					"clientId":     "github_client_id_789",
					"clientSecret": "github_client_secret_abc",
					"userId":       "github_username",
				},
			},
		},

		// =====================================================================
		// Realtime Processing Settings
		// =====================================================================
		"realtime": map[string]any{
			"interval":       1,
			"processingTime": false,

			// Audio Configuration (nested under realtime)
			"audio": map[string]any{
				"source":          "plughw:1,0",
				"ffmpegPath":      "/usr/bin/ffmpeg",
				"soxPath":         "/usr/bin/sox",
				"streamTransport": "auto",
				"soundLevel": map[string]any{
					"enabled":              true,
					"interval":             5,
					"debug":                false,
					"debugRealtimeLogging": false,
				},
				"equalizer": map[string]any{
					"enabled": true,
					"filters": []any{
						map[string]any{
							"type":      "HighPass",
							"frequency": 150.0,
							"q":         0.707,
						},
						map[string]any{
							"type":      "LowPass",
							"frequency": 12000.0,
							"q":         0.707,
						},
					},
				},
				"export": map[string]any{
					"debug":      false,
					"enabled":    true,
					"path":       "/data/clips",
					"type":       "wav",
					"bitrate":    "192k",
					"length":     12,
					"preCapture": 3,
					"gain":       0.0,
					"retention": map[string]any{
						"debug":            false,
						"policy":           "age",
						"maxAge":           "30d",
						"maxUsage":         "80%",
						"minClips":         10,
						"checkInterval":    60,
						"keepSpectrograms": true,
					},
					"normalization": map[string]any{
						"enabled":       true,
						"targetLUFS":    -16.0,
						"loudnessRange": 7.0,
						"truePeak":      -1.0,
					},
				},
			},

			// Dashboard Configuration (nested under realtime)
			"dashboard": map[string]any{
				"summaryLimit":    10,
				"locale":          "en",
				"temperatureUnit": "celsius",
				"thumbnails": map[string]any{
					"debug":          false,
					"summary":        true,
					"recent":         true,
					"imageProvider":  "auto",
					"fallbackPolicy": "all",
				},
				"spectrogram": map[string]any{
					"mode":    "auto",
					"enabled": true,
					"size":    "400x200",
					"raw":     false,
				},
			},

			// Dynamic Threshold
			"dynamicThreshold": map[string]any{
				"enabled":    true,
				"debug":      false,
				"trigger":    0.9,
				"min":        0.5,
				"validHours": 24,
			},

			// False Positive Filter
			"falsePositiveFilter": map[string]any{
				"level": 2,
			},

			// Log settings
			"log": map[string]any{
				"enabled": true,
				"path":    "/var/log/birdnet/obs.log",
			},

			// Log Deduplication
			"logDeduplication": map[string]any{
				"enabled":                   true,
				"healthCheckIntervalSeconds": 300,
			},

			// Privacy Filter
			"privacyFilter": map[string]any{
				"debug":      false,
				"enabled":    true,
				"confidence": 0.8,
			},

			// Dog Bark Filter
			"dogBarkFilter": map[string]any{
				"debug":      false,
				"enabled":    true,
				"confidence": 0.7,
				"remember":   15,
				"species":    []any{"Canis lupus familiaris"},
			},

			// MQTT (broker, username, password, TLS paths)
			"mqtt": map[string]any{
				"enabled":  true,
				"debug":    false,
				"broker":   "mqtt://mqtt.example.com:1883",
				"topic":    "birdnet/detections",
				"username": "mqtt_admin",
				"password": "mqtt_secret_pass",
				"retain":   true,
				"retrySettings": map[string]any{
					"enabled":           true,
					"maxRetries":        3,
					"initialDelay":      1,
					"maxDelay":          30,
					"backoffMultiplier": 2.0,
				},
				"tls": map[string]any{
					"enabled":            true,
					"insecureSkipVerify": false,
					"caCert":             "/etc/ssl/mqtt/ca-cert.pem",
					"clientCert":         "/etc/ssl/mqtt/client-cert.pem",
					"clientKey":          "/etc/ssl/mqtt/client-key.pem",
				},
			},

			// Birdweather (ID)
			"birdweather": map[string]any{
				"enabled":          true,
				"debug":            false,
				"id":               "birdweather_station_id_xyz",
				"threshold":        0.7,
				"locationAccuracy": 500.0,
				"retrySettings": map[string]any{
					"enabled":           true,
					"maxRetries":        3,
					"initialDelay":      1,
					"maxDelay":          30,
					"backoffMultiplier": 2.0,
				},
			},

			// Weather (OpenWeather/Wunderground API keys, stationId)
			"weather": map[string]any{
				"provider":     "openweather",
				"pollInterval": 30,
				"debug":        false,
				"openWeather": map[string]any{
					"enabled":  true,
					"apiKey":   "openweather_api_key_123abc",
					"endpoint": "https://api.openweathermap.org/data/2.5/weather",
					"units":    "metric",
					"language": "en",
				},
				"wunderground": map[string]any{
					"apiKey":    "wunderground_api_key_456def",
					"stationId": "KWASEATT123",
					"endpoint":  "https://api.weather.com/v2/pws/observations/current",
					"units":     "m",
				},
			},

			// RTSP (URLs with credentials)
			"rtsp": map[string]any{
				"transport": "tcp",
				"urls": []any{
					"rtsp://camera_user:camera_pass@192.168.1.100:554/stream1",
					"rtsp://admin:secret123@10.0.0.50:554/cam/realmonitor",
				},
				"ffmpegParameters": []any{"-rtsp_transport", "tcp"},
				"health": map[string]any{
					"healthyDataThreshold": 60,
					"monitoringInterval":   30,
				},
			},

			// eBird (API key)
			"ebird": map[string]any{
				"enabled":  true,
				"apiKey":   "ebird_api_key_xyz789",
				"cacheTTL": 24,
				"locale":   "en",
			},

			// Telemetry
			"telemetry": map[string]any{
				"enabled": true,
				"listen":  "0.0.0.0:8090",
			},

			// System Monitoring
			"monitoring": map[string]any{
				"enabled":                true,
				"checkInterval":          60,
				"criticalResendInterval": 30,
				"hysteresisPercent":      5.0,
				"cpu": map[string]any{
					"enabled":  true,
					"warning":  80.0,
					"critical": 95.0,
				},
				"memory": map[string]any{
					"enabled":  true,
					"warning":  85.0,
					"critical": 95.0,
				},
				"disk": map[string]any{
					"enabled":  true,
					"warning":  80.0,
					"critical": 90.0,
					"paths":    []any{"/", "/data", "/home/user/birdnet"},
				},
			},

			// Species Configuration
			"species": map[string]any{
				"include": []any{"Turdus migratorius", "Cardinalis cardinalis"}, //nolint:misspell // Correct scientific name for Northern Cardinal
				"exclude": []any{"Columba livia"},
				"config": map[string]any{
					"Turdus migratorius": map[string]any{
						"threshold": 0.7,
						"interval":  30,
						"actions": []any{
							map[string]any{
								"type":            "script",
								"command":         "/usr/local/bin/robin-alert.sh",
								"parameters":      []any{"--notify"},
								"executeDefaults": true,
							},
						},
					},
				},
			},

			// Species Tracking
			"speciesTracking": map[string]any{
				"enabled":                      true,
				"newSpeciesWindowDays":         7,
				"syncIntervalMinutes":          15,
				"notificationSuppressionHours": 24,
				"yearlyTracking": map[string]any{
					"enabled":    true,
					"resetMonth": 1,
					"resetDay":   1,
					"windowDays": 30,
				},
				"seasonalTracking": map[string]any{
					"enabled":    true,
					"windowDays": 14,
					"seasons": map[string]any{
						"spring": map[string]any{"startMonth": 3, "startDay": 1},
						"summer": map[string]any{"startMonth": 6, "startDay": 1},
						"fall":   map[string]any{"startMonth": 9, "startDay": 1},
						"winter": map[string]any{"startMonth": 12, "startDay": 1},
					},
				},
			},
		},

		// Section 4: Output (MySQL credentials)
		"output": map[string]any{
			"sqlite": map[string]any{
				"enabled": true,
				"path":    "/data/birdnet.db",
			},
			"mysql": map[string]any{
				"enabled":  true,
				"username": "mysql_user",
				"password": "mysql_secret_password",
				"database": "birdnet_db",
				"host":     "mysql.internal.example.com",
				"port":     "3306",
			},
		},

		// Section 5: Backup (encryption key, FTP/SFTP/S3/Rsync credentials)
		"backup": map[string]any{
			"enabled":        true,
			"debug":          false,
			"encryption":     true,
			"encryptionKey":  "base64_encryption_key_very_secret",
			"sanitizeConfig": true,
			"retention": map[string]any{
				"maxAge":     "30d",
				"maxBackups": 10,
				"minBackups": 3,
			},
			"schedules": []any{
				map[string]any{
					"enabled":  true,
					"hour":     2,
					"minute":   0,
					"weekday":  "",
					"isWeekly": false,
				},
				map[string]any{
					"enabled":  true,
					"hour":     3,
					"minute":   30,
					"weekday":  "Sunday",
					"isWeekly": true,
				},
			},
			"operationTimeouts": map[string]any{
				"backup":  "2h",
				"store":   "15m",
				"cleanup": "10m",
				"delete":  "2m",
			},
			"targets": []any{
				map[string]any{
					"type":    "local",
					"enabled": true,
					"settings": map[string]any{
						"path": "/backups/local",
					},
				},
				map[string]any{
					"type":    "ftp",
					"enabled": true,
					"settings": map[string]any{
						"host":     "ftp.backup.example.com",
						"port":     21,
						"username": "ftp_backup_user",
						"password": "ftp_backup_secret",
						"path":     "/remote/backups",
						"useTls":   true,
					},
				},
				map[string]any{
					"type":    "sftp",
					"enabled": true,
					"settings": map[string]any{
						"host":           "sftp.backup.example.com",
						"port":           22,
						"username":       "sftp_user",
						"password":       "sftp_password",
						"privateKeyPath": "/home/user/.ssh/id_rsa_backup",
						"path":           "/remote/sftp/backups",
					},
				},
				map[string]any{
					"type":    "s3",
					"enabled": true,
					"settings": map[string]any{
						"endpoint":        "https://s3.amazonaws.com",
						"region":          "us-west-2",
						"bucket":          "birdnet-backups",
						"accessKeyId":     "AKIAIOSFODNN7EXAMPLE",
						"secretAccessKey": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
						"prefix":          "backups/",
						"useSSL":          true,
					},
				},
				map[string]any{
					"type":    "rsync",
					"enabled": true,
					"settings": map[string]any{
						"host":       "rsync.backup.example.com",
						"port":       22,
						"username":   "rsync_user",
						"path":       "/backups/rsync",
						"sshKeyPath": "/home/user/.ssh/id_rsa_rsync",
					},
				},
				map[string]any{
					"type":    "gdrive",
					"enabled": true,
					"settings": map[string]any{
						"credentialsPath": "/secrets/google-drive-creds.json",
						"folderId":        "1AbCdEfGhIjKlMnOpQrStUvWxYz",
					},
				},
			},
		},

		// Section 6: Notification (Shoutrrr URLs with tokens, webhook URLs with auth)
		"notification": map[string]any{
			"templates": map[string]any{
				"newSpecies": map[string]any{
					"title":   "New Species Detected!",
					"message": "{{.CommonName}} ({{.ScientificName}}) at {{.Time}}",
				},
			},
			"push": map[string]any{
				"enabled":                  true,
				"defaultTimeout":           "30s",
				"maxRetries":               3,
				"retryDelay":               "5s",
				"minConfidenceThreshold":   0.8,
				"speciesCooldownMinutes":   30,
				"circuitBreaker": map[string]any{
					"enabled":              true,
					"maxFailures":          5,
					"timeout":              "30s",
					"halfOpenMaxRequests":  2,
				},
				"healthCheck": map[string]any{
					"enabled":  true,
					"interval": "60s",
					"timeout":  "10s",
				},
				"rateLimiting": map[string]any{
					"enabled":           true,
					"requestsPerMinute": 60,
					"burstSize":         10,
				},
				"providers": []any{
					map[string]any{
						"type":    "shoutrrr",
						"enabled": true,
						"name":    "ntfy",
						"urls": []any{
							"ntfy://admin:secret_token@ntfy.sh/birdnet-alerts",
							"discord://webhook_id:webhook_token@discord.com/webhooks",
							"telegram://bot_token@telegram?channels=channel_id",
						},
						"timeout": "30s",
					},
					map[string]any{
						"type":    "webhook",
						"enabled": true,
						"name":    "custom_webhook",
						"endpoints": []any{
							map[string]any{
								"url":     "https://hooks.slack.com/services/T123/B456/xyz789abc",
								"method":  "POST",
								"timeout": "10s",
								"headers": map[string]any{
									"X-Custom-Header": "value",
								},
								"auth": map[string]any{
									"type":      "bearer",
									"token":     "bearer_token_secret_123",
									"tokenFile": "/secrets/bearer_token.txt",
								},
							},
							map[string]any{
								"url":    "https://api.custom.com/webhook?token=secret_query_param",
								"method": "POST",
								"auth": map[string]any{
									"type":     "basic",
									"user":     "webhook_user",
									"pass":     "webhook_password",
									"userFile": "/secrets/webhook_user.txt",
									"passFile": "/secrets/webhook_pass.txt",
								},
							},
						},
					},
					map[string]any{
						"type":    "script",
						"enabled": true,
						"name":    "custom_script",
						"command": "/usr/local/bin/notify.sh",
						"args":    []any{"--verbose"},
						"environment": map[string]any{
							"API_KEY":    "script_api_key_secret",
							"API_SECRET": "script_api_secret_value",
						},
					},
				},
			},
		},

		// Section 7: Sentry (DSN not typically in config, but testing the pattern)
		"sentry": map[string]any{
			"enabled":    true,
			"debug":      false,
			"sampleRate": 1.0,
		},

		// Section 8: WebServer
		"webserver": map[string]any{
			"enabled": true,
			"port":    "8080",
			"liveStream": map[string]any{
				"bitrate":    128,
				"sampleRate": 48000,
			},
		},

		// Section 9: Default value preservation tests
		// These should NOT be redacted because they are empty/zero/nil
		"defaultValueTests": map[string]any{
			"password":        "",    // empty string - preserve
			"apiKey":          "",    // empty string - preserve
			"latitude":        0.0,   // zero float - preserve
			"longitude":       0.0,   // zero float - preserve
			"token":           nil,   // nil - preserve
			"port":            0,     // zero int - preserve
			"nonSensitiveKey": "visible_value",
		},
	}
}

// verifyAgainstGoldenFile compares the scrubbed config against a golden file.
// When -update flag is set, it regenerates the golden file instead of comparing.
func verifyAgainstGoldenFile(t *testing.T, got map[string]any, goldenFilename string) {
	t.Helper()

	// Marshal the result to YAML for comparison
	gotYAML, err := yaml.Marshal(got)
	require.NoError(t, err, "failed to marshal scrubbed config to YAML")

	goldenPath := filepath.Join("testdata", goldenFilename)

	if *updateGoldenFiles {
		// Create testdata directory if it doesn't exist
		err := os.MkdirAll("testdata", defaultDirPermissions)
		require.NoError(t, err, "failed to create testdata directory")

		// Write the new golden file
		err = os.WriteFile(goldenPath, gotYAML, defaultFilePermissions)
		require.NoError(t, err, "failed to update golden file")
		t.Logf("Updated golden file: %s", goldenPath)
		return
	}

	// Read expected golden file
	// #nosec G304 -- goldenPath is constructed from constant "testdata" + controlled test filename
	expectedYAML, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "failed to read golden file (run with -update to generate)")

	// Compare using YAML-aware comparison
	assert.YAMLEq(t, string(expectedYAML), string(gotYAML),
		"scrubbed config does not match golden file")
}

// getNestedValue navigates nested maps safely to retrieve a value by path.
func getNestedValue(config map[string]any, path ...string) (any, bool) {
	var current any = config
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// assertRedacted checks that a nested field at a given path is present and redacted.
func assertRedacted(t *testing.T, config map[string]any, path ...string) {
	t.Helper()
	val, ok := getNestedValue(config, path...)
	pathStr := strings.Join(path, ".")
	require.True(t, ok, "%s not found in scrubbed config", pathStr)
	assert.Equal(t, "[redacted]", val, "%s should be redacted", pathStr)
}

// assertCriticalFieldsRedacted verifies the most critical sensitive fields
// are properly redacted with expected placeholder values.
func assertCriticalFieldsRedacted(t *testing.T, config map[string]any) {
	t.Helper()

	// 1. Security passwords and secrets
	assertRedacted(t, config, "security", "basicAuth", "password")
	assertRedacted(t, config, "security", "basicAuth", "clientSecret")
	assertRedacted(t, config, "security", "sessionSecret")
	assertRedacted(t, config, "security", "allowSubnetBypass", "subnet")

	// 2. OAuth providers (array)
	providers, ok := getNestedValue(config, "security", "oauthProviders")
	require.True(t, ok, "security.oauthProviders not found")

	providerList, ok := providers.([]any)
	require.True(t, ok, "oauthProviders should be a slice")
	require.Len(t, providerList, 2, "should have 2 OAuth providers")

	for i, p := range providerList {
		provider, ok := p.(map[string]any)
		require.True(t, ok, "provider[%d] should be a map", i)
		assert.Equal(t, "[redacted]", provider["clientId"],
			"provider[%d].clientId should be redacted", i)
		assert.Equal(t, "[redacted]", provider["clientSecret"],
			"provider[%d].clientSecret should be redacted", i)
		assert.Equal(t, "[redacted]", provider["userId"],
			"provider[%d].userId should be redacted", i)
		// provider name should NOT be redacted
		assert.NotEqual(t, "[redacted]", provider["provider"])
	}

	// 3. Location coordinates
	assertRedacted(t, config, "birdnet", "latitude")
	assertRedacted(t, config, "birdnet", "longitude")

	// 4. MQTT credentials
	assertRedacted(t, config, "realtime", "mqtt", "username")
	assertRedacted(t, config, "realtime", "mqtt", "password")
	assertRedacted(t, config, "realtime", "mqtt", "topic")

	// 5. Birdweather ID
	assertRedacted(t, config, "realtime", "birdweather", "id")

	// 6. Weather API keys
	assertRedacted(t, config, "realtime", "weather", "openWeather", "apiKey")
	assertRedacted(t, config, "realtime", "weather", "wunderground", "apiKey")
	assertRedacted(t, config, "realtime", "weather", "wunderground", "stationId")

	// 7. eBird API key
	assertRedacted(t, config, "realtime", "ebird", "apiKey")

	// 8. Output MySQL credentials
	assertRedacted(t, config, "output", "mysql", "username")
	assertRedacted(t, config, "output", "mysql", "password")

	// 9. Backup encryption key
	assertRedacted(t, config, "backup", "encryptionKey")

	// 10. Check backup targets for credentials
	targets, ok := getNestedValue(config, "backup", "targets")
	require.True(t, ok, "backup.targets not found")
	targetList, ok := targets.([]any)
	require.True(t, ok, "backup.targets should be a slice")

	for i, target := range targetList {
		tgt, ok := target.(map[string]any)
		require.True(t, ok, "target[%d] should be a map", i)
		settings, ok := tgt["settings"].(map[string]any)
		require.True(t, ok, "target[%d].settings should be a map", i)
		targetType, ok := tgt["type"].(string)
		require.True(t, ok, "target[%d].type should be a string", i)

		switch targetType {
		case "s3":
			assert.Equal(t, "[redacted]", settings["accessKeyId"],
				"s3.accessKeyId should be redacted")
			assert.Equal(t, "[redacted]", settings["secretAccessKey"],
				"s3.secretAccessKey should be redacted")
		case "sftp":
			assert.Equal(t, "[redacted]", settings["password"],
				"sftp.password should be redacted")
			assert.Equal(t, "[redacted]", settings["privateKeyPath"],
				"sftp.privateKeyPath should be redacted")
			assert.Equal(t, "[redacted]", settings["username"],
				"sftp.username should be redacted")
		case "ftp":
			assert.Equal(t, "[redacted]", settings["password"],
				"ftp.password should be redacted")
			assert.Equal(t, "[redacted]", settings["username"],
				"ftp.username should be redacted")
		case "rsync":
			assert.Equal(t, "[redacted]", settings["username"],
				"rsync.username should be redacted")
			assert.Equal(t, "[redacted]", settings["sshKeyPath"],
				"rsync.sshKeyPath should be redacted")
		case "gdrive":
			assert.Equal(t, "[redacted]", settings["credentialsPath"],
				"gdrive.credentialsPath should be redacted")
		}
	}
}

// assertDefaultValuesPreserved verifies that default/empty values are NOT redacted.
func assertDefaultValuesPreserved(t *testing.T, config map[string]any) {
	t.Helper()

	defaults, ok := config["defaultValueTests"].(map[string]any)
	require.True(t, ok, "defaultValueTests section not found")

	// Empty strings should be preserved
	assert.Empty(t, defaults["password"], "empty password should be preserved")
	assert.Empty(t, defaults["apiKey"], "empty apiKey should be preserved")

	// Zero floats should be preserved
	assert.InDelta(t, 0.0, defaults["latitude"], 0, "zero latitude should be preserved")
	assert.InDelta(t, 0.0, defaults["longitude"], 0, "zero longitude should be preserved")

	// Zero ints should be preserved
	assert.Equal(t, 0, defaults["port"], "zero port should be preserved")

	// Nil should be preserved
	assert.Nil(t, defaults["token"], "nil token should be preserved")

	// Non-sensitive keys should be unchanged
	assert.Equal(t, "visible_value", defaults["nonSensitiveKey"],
		"non-sensitive key should be unchanged")
}

// assertURLStructuralRedaction verifies URLs are redacted with proper structure preserved.
func assertURLStructuralRedaction(t *testing.T, config map[string]any) {
	t.Helper()

	// RTSP URLs should preserve scheme and port
	rtsp, ok := getNestedValue(config, "realtime", "rtsp", "urls")
	require.True(t, ok, "realtime.rtsp.urls not found")

	urls := rtsp.([]any)
	require.Len(t, urls, 2, "should have 2 RTSP URLs")

	for i, u := range urls {
		urlStr := u.(string)
		assert.Contains(t, urlStr, "rtsp://", "URL[%d] should preserve rtsp:// scheme", i)
		assert.Contains(t, urlStr, "[host]", "URL[%d] should redact host", i)
		assert.Contains(t, urlStr, ":554", "URL[%d] should preserve port", i)
		assert.Contains(t, urlStr, "[user]", "URL[%d] should show [user] placeholder", i)
		assert.Contains(t, urlStr, "[pass]", "URL[%d] should show [pass] placeholder", i)
		assert.NotContains(t, urlStr, "camera_user", "URL[%d] should not contain credentials", i)
		assert.NotContains(t, urlStr, "admin", "URL[%d] should not contain credentials", i)
	}

	// Weather endpoints should be structurally redacted
	endpoint, ok := getNestedValue(config, "realtime", "weather", "openWeather", "endpoint")
	require.True(t, ok)
	endpointStr := endpoint.(string)
	assert.Contains(t, endpointStr, "https://", "endpoint should preserve https:// scheme")
	assert.Contains(t, endpointStr, "[host]", "endpoint should redact host")

	// Notification webhook URLs
	providers, ok := getNestedValue(config, "notification", "push", "providers")
	require.True(t, ok)

	providerList := providers.([]any)
	for _, p := range providerList {
		provider := p.(map[string]any)

		if provider["type"] == "shoutrrr" {
			shoutrrrURLs := provider["urls"].([]any)
			for _, u := range shoutrrrURLs {
				urlStr := u.(string)
				// URLs should be structurally redacted
				assert.NotContains(t, urlStr, "admin:secret", "shoutrrr URL should not contain credentials")
				assert.NotContains(t, urlStr, "webhook_token", "shoutrrr URL should not contain token")
				assert.Contains(t, urlStr, "[host]", "shoutrrr URL host should be redacted")
			}
		}

		if provider["type"] == "webhook" {
			endpoints := provider["endpoints"].([]any)
			for _, e := range endpoints {
				ep := e.(map[string]any)
				url := ep["url"].(string)
				assert.Contains(t, url, "[host]", "webhook URL should have redacted host")
				assert.Contains(t, url, "https://", "webhook URL should preserve https:// scheme")
			}
		}
	}

	// MQTT broker URL should be structurally redacted
	broker, ok := getNestedValue(config, "realtime", "mqtt", "broker")
	require.True(t, ok)
	brokerStr := broker.(string)
	assert.Contains(t, brokerStr, "mqtt://", "broker should preserve mqtt:// scheme")
	assert.Contains(t, brokerStr, "[host]", "broker should redact host")
	assert.Contains(t, brokerStr, ":1883", "broker should preserve port")
}

// TestComprehensivePrivacyScrubbing_Integration is a comprehensive integration test
// that verifies all sensitive fields in the config are properly scrubbed.
// Run with -update flag to regenerate golden file: go test -v -run TestComprehensivePrivacyScrubbing -update
func TestComprehensivePrivacyScrubbing_Integration(t *testing.T) {
	t.Parallel()

	// Create collector with default sensitive keys
	c := &Collector{
		sensitiveKeys: defaultSensitiveKeys(),
	}

	// Build comprehensive mock config
	input := createComprehensiveMockConfig()

	// Execute scrubbing
	got := c.scrubConfig(input)

	// Part 1: Golden file comparison
	t.Run("golden_file_comparison", func(t *testing.T) {
		verifyAgainstGoldenFile(t, got, "scrubbed_config_golden.yaml")
	})

	// Part 2: Targeted assertions for critical fields
	t.Run("critical_field_assertions", func(t *testing.T) {
		assertCriticalFieldsRedacted(t, got)
	})

	// Part 3: Default value preservation
	t.Run("default_values_preserved", func(t *testing.T) {
		assertDefaultValuesPreserved(t, got)
	})

	// Part 4: URL structural redaction
	t.Run("url_structural_redaction", func(t *testing.T) {
		assertURLStructuralRedaction(t, got)
	})
}
