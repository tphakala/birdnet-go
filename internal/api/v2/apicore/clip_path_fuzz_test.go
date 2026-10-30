// clip_path_fuzz_test.go: Fuzz test for clip path normalization.

package apicore

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// FuzzNormalizeClipPathStrict tests clip path normalization with various inputs.
func FuzzNormalizeClipPathStrict(f *testing.F) {
	seedInputs := []struct {
		path   string
		prefix string
	}{
		// Valid paths
		{"clips/2024/01/bird.wav", "clips/"},
		{"2024/01/bird.wav", "clips/"},
		{"clips/", "clips/"},
		// Path traversal
		{"../etc/passwd", "clips/"},
		{"clips/../../../etc/passwd", "clips/"},
		{"..%2f..%2f..%2fetc%2fpasswd", "clips/"},
		// Absolute paths
		{"/etc/passwd", "clips/"},
		{"/absolute/path", "clips/"},
		// Backslashes
		{"clips\\2024\\01\\bird.wav", "clips/"},
		{"..\\..\\etc\\passwd", "clips/"},
		// Empty and special
		{"", "clips/"},
		{".", "clips/"},
		{"..", "clips/"},
		// Case variations
		{"CLIPS/2024/file.wav", "clips/"},
		{"Clips/2024/file.wav", "clips/"},
		// Different prefixes
		{"audio/2024/file.wav", "audio/"},
		{"data/clips/file.wav", "data/clips/"},
		// Null bytes
		{"clips/file\x00.wav", "clips/"},
		// Unicode
		{"clips/файл.wav", "clips/"},
		{"clips/鳥.wav", "clips/"},
	}

	for _, s := range seedInputs {
		f.Add(s.path, s.prefix)
	}

	f.Fuzz(func(t *testing.T, path, prefix string) {
		// Should never panic
		result, valid := NormalizeClipPathStrict(path, prefix)

		if valid {
			// Valid results should not contain path traversal
			assert.NotContains(t, result, "..", "Valid result must not contain ..")

			// Valid results should not be absolute paths
			if result != "" {
				assert.False(t, strings.HasPrefix(result, "/"), "Valid result should not be absolute")
			}
		}
	})
}
