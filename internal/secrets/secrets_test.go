package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		envVars map[string]string
		want    string
		wantErr bool
	}{
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: false,
		},
		{
			name:    "literal string",
			input:   "literal-value",
			want:    "literal-value",
			wantErr: false,
		},
		{
			name:    "simple variable expansion",
			input:   "${TOKEN}",
			envVars: map[string]string{"TOKEN": "secret123"},
			want:    "secret123",
			wantErr: false,
		},
		{
			name:    "variable with prefix and suffix",
			input:   "Bearer ${TOKEN}",
			envVars: map[string]string{"TOKEN": "abc123"},
			want:    "Bearer abc123",
			wantErr: false,
		},
		{
			name:    "multiple variables",
			input:   "${USER}:${PASS}",
			envVars: map[string]string{"USER": "admin", "PASS": "secret"},
			want:    "admin:secret",
			wantErr: false,
		},
		{
			name:    "default value syntax - var exists",
			input:   "${TOKEN:-default}",
			envVars: map[string]string{"TOKEN": "actual"},
			want:    "actual",
			wantErr: false,
		},
		{
			name:    "default value syntax - var missing",
			input:   "${TOKEN:-default}",
			envVars: map[string]string{},
			want:    "default",
			wantErr: false,
		},
		{
			name:    "default value with special chars",
			input:   "${TOKEN:-my-default-123}",
			envVars: map[string]string{},
			want:    "my-default-123",
			wantErr: false,
		},
		{
			name:    "missing required variable",
			input:   "${MISSING_TOKEN}",
			envVars: map[string]string{},
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty default fallback",
			input:   "${OPTIONAL_TOKEN:-}",
			envVars: map[string]string{},
			want:    "",
			wantErr: false,
		},
		{
			name:    "partial expansion with missing var",
			input:   "prefix-${MISSING}-suffix",
			envVars: map[string]string{},
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			got, err := ExpandString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExpandString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExpandString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadFile(t *testing.T) {
	// Create temp directory for test files
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		setup       func() string // Returns file path
		wantContent string
		wantErr     bool
		errContains string
	}{
		{
			name: "valid secret file",
			setup: func() string {
				path := filepath.Join(tmpDir, "valid_secret")
				if err := os.WriteFile(path, []byte("my-secret-token"), 0o400); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantContent: "my-secret-token",
			wantErr:     false,
		},
		{
			name: "secret with trailing newline",
			setup: func() string {
				path := filepath.Join(tmpDir, "secret_with_newline")
				if err := os.WriteFile(path, []byte("secret123\n"), 0o400); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantContent: "secret123",
			wantErr:     false,
		},
		{
			name: "secret with whitespace preserved",
			setup: func() string {
				path := filepath.Join(tmpDir, "secret_whitespace")
				if err := os.WriteFile(path, []byte("  token  \n\n"), 0o400); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantContent: "  token  ", // Leading/trailing spaces preserved, only newlines trimmed
			wantErr:     false,
		},
		{
			name: "permissive permissions warning",
			setup: func() string {
				path := filepath.Join(tmpDir, "permissive_secret")
				if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantContent: "secret",
			wantErr:     false, // Warning but not error
		},
		{
			name: "empty file path",
			setup: func() string {
				return ""
			},
			wantErr:     true,
			errContains: "empty",
		},
		{
			name: "file does not exist",
			setup: func() string {
				return filepath.Join(tmpDir, "nonexistent")
			},
			wantErr:     true,
			errContains: "not found",
		},
		{
			name: "empty secret file",
			setup: func() string {
				path := filepath.Join(tmpDir, "empty_secret")
				if err := os.WriteFile(path, []byte(""), 0o400); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantErr:     true,
			errContains: "empty",
		},
		{
			name: "directory instead of file",
			setup: func() string {
				path := filepath.Join(tmpDir, "directory_secret")
				if err := os.Mkdir(path, 0o750); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantErr:     true,
			errContains: "not a regular file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup()
			got, err := ReadFile(path)

			if (err != nil) != tt.wantErr {
				t.Errorf("ReadFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ReadFile() error = %v, want error containing %q", err, tt.errContains)
				}
			}

			if !tt.wantErr && got != tt.wantContent {
				t.Errorf("ReadFile() = %q, want %q", got, tt.wantContent)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	tmpDir := t.TempDir()
	secretFile := filepath.Join(tmpDir, "secret")
	if err := os.WriteFile(secretFile, []byte("file-secret\n"), 0o400); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		filePath string
		value    string
		envVars  map[string]string
		want     string
		wantErr  bool
	}{
		{
			name:    "literal value only",
			value:   "literal-token",
			want:    "literal-token",
			wantErr: false,
		},
		{
			name:    "env var expansion",
			value:   "${TOKEN}",
			envVars: map[string]string{"TOKEN": "env-token"},
			want:    "env-token",
			wantErr: false,
		},
		{
			name:     "file takes precedence over value",
			filePath: secretFile,
			value:    "ignored-value",
			want:     "file-secret",
			wantErr:  false,
		},
		{
			name:     "file only",
			filePath: secretFile,
			want:     "file-secret",
			wantErr:  false,
		},
		{
			name:    "neither file nor value",
			want:    "",
			wantErr: false,
		},
		{
			name:     "invalid file path",
			filePath: "/nonexistent/secret",
			wantErr:  true,
		},
		{
			name:    "missing env var",
			value:   "${MISSING}",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			got, err := Resolve(tt.filePath, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMustResolve(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		filePath  string
		value     string
		envVars   map[string]string
		want      string
		wantErr   bool
	}{
		{
			name:      "value provided",
			fieldName: "token",
			value:     "secret",
			want:      "secret",
			wantErr:   false,
		},
		{
			name:      "no value provided",
			fieldName: "token",
			want:      "",
			wantErr:   true,
		},
		{
			name:      "env var provided",
			fieldName: "token",
			value:     "${TOKEN}",
			envVars:   map[string]string{"TOKEN": "secret"},
			want:      "secret",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			got, err := MustResolve(tt.fieldName, tt.filePath, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("MustResolve() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("MustResolve() = %q, want %q", got, tt.want)
			}
		})
	}
}
