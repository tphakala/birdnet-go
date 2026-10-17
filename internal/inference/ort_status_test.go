package inference

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestORTVersionMeetsMinimum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		version   string
		wantMeets bool
		wantKnown bool
	}{
		{name: "exact minimum", version: "1.29.0", wantMeets: true, wantKnown: true},
		{name: "minimum without patch", version: "1.29", wantMeets: true, wantKnown: true},
		{name: "current pin", version: "1.31.0", wantMeets: true, wantKnown: true},
		{name: "release candidate", version: "1.29.0-rc1", wantMeets: true, wantKnown: true},
		{name: "rc without patch", version: "1.29rc1", wantMeets: true, wantKnown: true},
		{name: "three digit minor", version: "1.100.0", wantMeets: true, wantKnown: true},
		{name: "major bump", version: "2.0.0", wantMeets: true, wantKnown: true},
		{name: "surrounding spaces", version: " 1.31.0 ", wantMeets: true, wantKnown: true},
		{name: "previous minor", version: "1.28.2", wantMeets: false, wantKnown: true},
		{name: "old pin", version: "1.25.1", wantMeets: false, wantKnown: true},
		{name: "major zero", version: "0.99", wantMeets: false, wantKnown: true},
		{name: "numeric compare, not string compare", version: "1.3.0", wantMeets: false, wantKnown: true},
		{name: "major only", version: "1", wantMeets: false, wantKnown: false},
		{name: "empty string", version: "", wantMeets: false, wantKnown: false},
		{name: "garbage", version: "abc", wantMeets: false, wantKnown: false},
		{name: "non numeric minor", version: "1.x", wantMeets: false, wantKnown: false},
		{name: "v prefix", version: "v1.31.0", wantMeets: false, wantKnown: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meets, known := ortVersionMeetsMinimum(tt.version)
			assert.Equal(t, tt.wantMeets, meets, "meets for %q", tt.version)
			assert.Equal(t, tt.wantKnown, known, "known for %q", tt.version)
		})
	}
}

// writeLibFile creates an empty file named name inside dir and returns its path.
func writeLibFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	return path
}

func TestCheckORTAvailability_GatesOnFileNameVersion(t *testing.T) {
	// Not parallel: once ORT is initialized the fast path ignores the path.
	if IsORTInitialized() {
		t.Skip("ORT already initialized in this process; the fast path ignores the path")
	}

	t.Run("old versioned file is rejected", func(t *testing.T) {
		path := writeLibFile(t, t.TempDir(), "libonnxruntime.so.1.25.1")
		status := CheckORTAvailability(path)
		assert.False(t, status.Available)
		assert.Equal(t, "1.25.1", status.Version)
		assert.Contains(t, status.Error, "1.25.1")
		assert.Contains(t, status.Error, "1.29 or newer")
	})

	t.Run("previous minor is rejected", func(t *testing.T) {
		path := writeLibFile(t, t.TempDir(), "libonnxruntime.so.1.28.2")
		assert.False(t, CheckORTAvailability(path).Available)
	})

	t.Run("minimum is accepted", func(t *testing.T) {
		path := writeLibFile(t, t.TempDir(), "libonnxruntime.so.1.29.0")
		status := CheckORTAvailability(path)
		assert.True(t, status.Available)
		assert.Empty(t, status.Error)
	})

	t.Run("symlink chain to current pin is accepted", func(t *testing.T) {
		dir := t.TempDir()
		writeLibFile(t, dir, "libonnxruntime.so.1.31.0")
		require.NoError(t, os.Symlink("libonnxruntime.so.1.31.0", filepath.Join(dir, "libonnxruntime.so.1")))
		link := filepath.Join(dir, "libonnxruntime.so")
		require.NoError(t, os.Symlink("libonnxruntime.so.1", link))
		status := CheckORTAvailability(link)
		assert.True(t, status.Available)
		assert.Equal(t, "1.31.0", status.Version)
	})

	t.Run("unversioned so.1 is optimistic", func(t *testing.T) {
		path := writeLibFile(t, t.TempDir(), "libonnxruntime.so.1")
		status := CheckORTAvailability(path)
		assert.True(t, status.Available)
		assert.Equal(t, "1", status.Version)
		assert.Empty(t, status.Error)
	})

	t.Run("unversioned dylib is accepted", func(t *testing.T) {
		path := writeLibFile(t, t.TempDir(), "libonnxruntime.dylib")
		status := CheckORTAvailability(path)
		assert.True(t, status.Available)
		assert.Empty(t, status.Version)
	})
}

func TestCheckORTAvailability_FollowsSymlinkLayoutTransitions(t *testing.T) {
	// Not parallel: same fast-path guard as above.
	if IsORTInitialized() {
		t.Skip("ORT already initialized in this process; the fast path ignores the path")
	}

	// relink replaces the symlink at dir/name with one pointing at target.
	relink := func(t *testing.T, dir, name, target string) {
		t.Helper()
		link := filepath.Join(dir, name)
		require.NoError(t, os.RemoveAll(link))
		require.NoError(t, os.Symlink(target, link))
	}

	t.Run("cp -P upgrade then downgrade", func(t *testing.T) {
		dir := t.TempDir()
		lib := filepath.Join(dir, "libonnxruntime.so")
		writeLibFile(t, dir, "libonnxruntime.so.1.25.1")
		relink(t, dir, "libonnxruntime.so.1", "libonnxruntime.so.1.25.1")
		relink(t, dir, "libonnxruntime.so", "libonnxruntime.so.1")
		assert.False(t, CheckORTAvailability(lib).Available, "old chain")

		writeLibFile(t, dir, "libonnxruntime.so.1.31.0")
		relink(t, dir, "libonnxruntime.so.1", "libonnxruntime.so.1.31.0")
		upgraded := CheckORTAvailability(lib)
		assert.True(t, upgraded.Available, "after cp -P upgrade")
		assert.Equal(t, "1.31.0", upgraded.Version)

		require.NoError(t, os.Remove(filepath.Join(dir, "libonnxruntime.so.1.31.0")))
		relink(t, dir, "libonnxruntime.so.1", "libonnxruntime.so.1.25.1")
		assert.False(t, CheckORTAvailability(lib).Available, "after downgrade")
	})

	t.Run("regular files then symlink chain", func(t *testing.T) {
		dir := t.TempDir()
		lib := filepath.Join(dir, "libonnxruntime.so")
		for _, name := range []string{"libonnxruntime.so", "libonnxruntime.so.1", "libonnxruntime.so.1.25.1"} {
			writeLibFile(t, dir, name)
		}
		assert.True(t, CheckORTAvailability(lib).Available, "plain regular files carry no version")

		require.NoError(t, os.Remove(lib))
		require.NoError(t, os.Remove(filepath.Join(dir, "libonnxruntime.so.1")))
		writeLibFile(t, dir, "libonnxruntime.so.1.31.0")
		relink(t, dir, "libonnxruntime.so.1", "libonnxruntime.so.1.31.0")
		relink(t, dir, "libonnxruntime.so", "libonnxruntime.so.1")
		status := CheckORTAvailability(lib)
		assert.True(t, status.Available)
		assert.Equal(t, "1.31.0", status.Version)
	})
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

	t.Run("unversioned so.1 regular file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "libonnxruntime.so.1")
		require.NoError(t, os.WriteFile(path, nil, 0o644))
		assert.Equal(t, "1", inferVersionFromPath(path))
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
		dylib := filepath.Join(dir, "libonnxruntime.1.25.1.dylib")
		require.NoError(t, os.WriteFile(dylib, nil, 0o644))
		assert.Equal(t, "1.25.1", inferVersionFromPath(dylib))
	})

	t.Run("macOS dylib symlink to versioned", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		versioned := filepath.Join(dir, "libonnxruntime.1.25.1.dylib")
		link := filepath.Join(dir, "libonnxruntime.dylib")
		require.NoError(t, os.WriteFile(versioned, nil, 0o644))
		require.NoError(t, os.Symlink(versioned, link))
		assert.Equal(t, "1.25.1", inferVersionFromPath(link))
	})

	t.Run("macOS dylib with dotted prefix", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		dylib := filepath.Join(dir, "my.custom.lib.1.25.1.dylib")
		require.NoError(t, os.WriteFile(dylib, nil, 0o644))
		assert.Equal(t, "1.25.1", inferVersionFromPath(dylib))
	})

	t.Run("macOS dylib without version", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		dylib := filepath.Join(dir, "libonnxruntime.dylib")
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
		assert.Empty(t, versionError("1.31.0"))
	})

	t.Run("incompatible version mentions both versions", func(t *testing.T) {
		t.Parallel()
		msg := versionError("1.24.4")
		assert.Contains(t, msg, "1.24.4")
		assert.Contains(t, msg, "older than")
		assert.Contains(t, msg, "1.29 or newer")
	})

	t.Run("empty version mentions unknown", func(t *testing.T) {
		t.Parallel()
		msg := versionError("")
		assert.Contains(t, msg, "unable to determine")
		assert.Contains(t, msg, "1.29 or newer")
	})
}

func TestLibraryFileExists(t *testing.T) {
	t.Parallel()

	t.Run("real file with directory component", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fakeLib := filepath.Join(dir, "onnxruntime.dll")
		require.NoError(t, os.WriteFile(fakeLib, nil, 0o644))
		assert.True(t, libraryFileExists(fakeLib), "libraryFileExists(%q) should be true for existing file", fakeLib)
	})

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "empty string", path: "", want: false},
		{name: "bare dlopen name", path: "onnxruntime", want: false},
		{name: "bare so name", path: "libonnxruntime.so", want: false},
		{name: "bare dll name", path: "onnxruntime.dll", want: false},
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
	assert.Equal(t, "1.29 or newer", ORTRequiredVersion())
}

func TestORTMinimumVersion(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "1.29", ORTMinimumVersion())
}
