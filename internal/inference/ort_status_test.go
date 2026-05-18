package inference

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsVersionCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{name: "patch 1", version: "1.25.1", want: true},
		{name: "patch 0", version: "1.25.0", want: true},
		{name: "patch 99", version: "1.25.99", want: true},
		{name: "exact major.minor", version: "1.25", want: true},
		{name: "older minor", version: "1.24.4", want: false},
		{name: "newer minor", version: "1.26.0", want: false},
		{name: "major bump", version: "2.0.0", want: false},
		{name: "empty string", version: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isVersionCompatible(tt.version)
			assert.Equal(t, tt.want, got, "isVersionCompatible(%q)", tt.version)
		})
	}
}

func TestInferVersionFromPath(t *testing.T) {
	t.Parallel()

	t.Run("versioned so filename", func(t *testing.T) {
		t.Parallel()
		// Create a real file so EvalSymlinks does not fail.
		dir := t.TempDir()
		versioned := dir + "/libonnxruntime.so.1.25.1"
		require.NoError(t, os.WriteFile(versioned, nil, 0o644))
		assert.Equal(t, "1.25.1", inferVersionFromPath(versioned))
	})

	t.Run("unversioned so without symlink", func(t *testing.T) {
		t.Parallel()
		// A standalone .so file with no version suffix and no symlink target.
		dir := t.TempDir()
		plain := dir + "/libonnxruntime.so"
		require.NoError(t, os.WriteFile(plain, nil, 0o644))
		assert.Empty(t, inferVersionFromPath(plain))
	})

	t.Run("symlink to versioned so", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		versioned := dir + "/libonnxruntime.so.1.25.1"
		link := dir + "/libonnxruntime.so"
		require.NoError(t, os.WriteFile(versioned, nil, 0o644))
		require.NoError(t, os.Symlink(versioned, link))
		assert.Equal(t, "1.25.1", inferVersionFromPath(link))
	})

	t.Run("macOS dylib with version", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		dylib := dir + "/libonnxruntime.1.25.1.dylib"
		require.NoError(t, os.WriteFile(dylib, nil, 0o644))
		assert.Equal(t, "1.25.1", inferVersionFromPath(dylib))
	})

	t.Run("macOS dylib symlink to versioned", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		versioned := dir + "/libonnxruntime.1.25.1.dylib"
		link := dir + "/libonnxruntime.dylib"
		require.NoError(t, os.WriteFile(versioned, nil, 0o644))
		require.NoError(t, os.Symlink(versioned, link))
		assert.Equal(t, "1.25.1", inferVersionFromPath(link))
	})

	t.Run("macOS dylib with dotted prefix", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		dylib := dir + "/my.custom.lib.1.25.1.dylib"
		require.NoError(t, os.WriteFile(dylib, nil, 0o644))
		assert.Equal(t, "1.25.1", inferVersionFromPath(dylib))
	})

	t.Run("macOS dylib without version", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		dylib := dir + "/libonnxruntime.dylib"
		require.NoError(t, os.WriteFile(dylib, nil, 0o644))
		assert.Empty(t, inferVersionFromPath(dylib))
	})

	t.Run("windows dll", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, inferVersionFromPath("C:\\onnxruntime.dll"))
	})

	t.Run("empty string", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, inferVersionFromPath(""))
	})
}

func TestVersionError(t *testing.T) {
	t.Parallel()

	t.Run("compatible version returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, versionError("1.25.1"))
	})

	t.Run("incompatible version mentions both versions", func(t *testing.T) {
		t.Parallel()
		msg := versionError("1.24.4")
		assert.Contains(t, msg, "1.24.4")
		assert.Contains(t, msg, "1.25.x")
	})

	t.Run("empty version mentions unknown", func(t *testing.T) {
		t.Parallel()
		msg := versionError("")
		assert.Contains(t, msg, "unable to determine")
		assert.Contains(t, msg, "1.25.x")
	})
}

func TestLibraryFileExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "empty string", path: "", want: false},
		{name: "bare dlopen name", path: "onnxruntime", want: false},
		{name: "bare so name", path: "libonnxruntime.so", want: false},
		{name: "nonexistent absolute path", path: "/nonexistent/path/libonnxruntime.so", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := libraryFileExists(tt.path)
			assert.Equal(t, tt.want, got, "libraryFileExists(%q)", tt.path)
		})
	}
}

func TestORTRequiredVersion(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "1.25.x", ORTRequiredVersion())
}
