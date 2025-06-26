package support

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

			if got := lfc.isLogFile(tt.filename); got != tt.want {
				t.Errorf("isLogFile(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

// TestLogFileCollector_isFileWithinTimeRange tests time range checking
func TestLogFileCollector_isFileWithinTimeRange(t *testing.T) {
	now := time.Now()
	lfc := &logFileCollector{
		cutoffTime: now.Add(-24 * time.Hour), // 24 hours ago
	}

	tests := []struct {
		name    string
		modTime time.Time
		want    bool
	}{
		{"recent file", now.Add(-1 * time.Hour), true},
		{"file at cutoff", now.Add(-24 * time.Hour), true},
		{"old file", now.Add(-48 * time.Hour), false},
		{"future file", now.Add(1 * time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock FileInfo
			info := &mockFileInfo{modTime: tt.modTime}
			if got := lfc.isFileWithinTimeRange(info); got != tt.want {
				t.Errorf("isFileWithinTimeRange() = %v, want %v", got, tt.want)
			}
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
		{"within limit", 1000, 5000, 1000, true},
		{"exactly at limit", 4000, 5000, 1000, true},
		{"exceeds limit", 4500, 5000, 1000, false},
		{"zero file size", 1000, 5000, 0, true},
		{"already at max", 5000, 5000, 1000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lfc := &logFileCollector{
				totalSize: tt.totalSize,
				maxSize:   tt.maxSize,
			}
			if got := lfc.canAddFile(tt.fileSize); got != tt.want {
				t.Errorf("canAddFile(%d) = %v, want %v", tt.fileSize, got, tt.want)
			}
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
	os.Chdir(tempDir)
	t.Cleanup(func() {
		os.Chdir(oldWd)
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
		if count > 1 {
			t.Errorf("Path %q appears %d times, expected 1", path, count)
		}
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
	w.Close()

	// Read the zip content
	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("Failed to read zip: %v", err)
	}

	// Look for README.txt
	found := false
	for _, f := range r.File {
		if f.Name != "logs/README.txt" {
			continue
		}

		found = true

		// Check content
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("Failed to open README: %v", err)
		}

		content, err := io.ReadAll(rc)
		rc.Close() // Close immediately after reading, not deferred

		if err != nil {
			t.Fatalf("Failed to read README: %v", err)
		}

		expected := "No log files were found or all logs were older than the specified duration."
		if string(content) != expected {
			t.Errorf("README content = %q, want %q", string(content), expected)
		}
		break
	}

	if !found {
		t.Error("README.txt not found in archive")
	}
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
				"password":   "[REDACTED]",
				"api_key":    "[REDACTED]",
				"safe_field": "visible",
				"nested": map[string]any{
					"token":        "[REDACTED]",
					"normal_field": "also_visible",
				},
			},
		},
		{
			name: "scrub array values",
			config: map[string]any{
				"urls": []any{"http://example.com", "http://secret.com"},
				"data": []any{"safe1", "safe2"},
			},
			want: map[string]any{
				"urls": "[REDACTED]",
				"data": []any{"safe1", "safe2"},
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
				"Password": "[REDACTED]",
				"API_KEY":  "[REDACTED]",
				"ApiToken": "[REDACTED]",
				"normal":   "visible",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.scrubConfig(tt.config)
			if !compareConfigs(got, tt.want) {
				t.Errorf("scrubConfig() = %v, want %v", got, tt.want)
			}
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
				if t1[i] != t2[i] {
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
