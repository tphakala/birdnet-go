package inference

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureVersion is the version the script tests install. It is not the pinned
// release: the tests carry their own pin file.
const fixtureVersion = "1.2.3"

// ortPlatform is the per-platform contract of scripts/fetch-onnxruntime.sh:
// where the libraries sit in each source and what the install directory holds.
// Format verbs are filled with the version.
type ortPlatform struct {
	id       string
	nugetRID string // empty when the NuGet package has no build
	asset    string // GitHub release archive name
	release  bool   // built for a release
	files    []string
	// installed are the names in the library directory, parallel to files.
	installed []string
	// links maps a link name to its target.
	links map[string]string
}

var ortPlatforms = []*ortPlatform{
	{
		id: "linux-amd64", nugetRID: "linux-x64", asset: "onnxruntime-linux-x64-%s.tgz", release: true,
		files:     []string{"libonnxruntime.so", "libonnxruntime_providers_shared.so"},
		installed: []string{"libonnxruntime.so.%s", "libonnxruntime_providers_shared.so"},
		links:     map[string]string{"libonnxruntime.so.1": "libonnxruntime.so.%s", "libonnxruntime.so": "libonnxruntime.so.1"},
	},
	{
		id: "linux-arm64", nugetRID: "linux-arm64", asset: "onnxruntime-linux-aarch64-%s.tgz", release: true,
		files:     []string{"libonnxruntime.so", "libonnxruntime_providers_shared.so"},
		installed: []string{"libonnxruntime.so.%s", "libonnxruntime_providers_shared.so"},
		links:     map[string]string{"libonnxruntime.so.1": "libonnxruntime.so.%s", "libonnxruntime.so": "libonnxruntime.so.1"},
	},
	{
		id: "darwin-arm64", nugetRID: "osx-arm64", asset: "onnxruntime-osx-arm64-%s.tgz", release: true,
		files:     []string{"libonnxruntime.dylib"},
		installed: []string{"libonnxruntime.%s.dylib"},
		links:     map[string]string{"libonnxruntime.dylib": "libonnxruntime.%s.dylib"},
	},
	{
		id: "windows-amd64", nugetRID: "win-x64", asset: "onnxruntime-win-x64-%s.zip", release: true,
		files:     []string{"onnxruntime.dll", "onnxruntime_providers_shared.dll"},
		installed: []string{"onnxruntime.dll", "onnxruntime_providers_shared.dll"},
	},
	{
		id: "darwin-amd64", asset: "onnxruntime-osx-x86_64-%s.tgz",
		files:     []string{"libonnxruntime.dylib"},
		installed: []string{"libonnxruntime.%s.dylib"},
		links:     map[string]string{"libonnxruntime.dylib": "libonnxruntime.%s.dylib"},
	},
}

// sprintVersion fills the version into a name from the table; names without a
// verb come back unchanged.
func sprintVersion(name string) string {
	if strings.Contains(name, "%s") {
		return fmt.Sprintf(name, fixtureVersion)
	}
	return name
}

func (p *ortPlatform) installedNames() []string {
	names := slices.Collect(maps.Keys(p.links))
	for _, n := range p.installed {
		names = append(names, sprintVersion(n))
	}
	slices.Sort(names)
	return names
}

func (p *ortPlatform) content(file string) []byte {
	return []byte("fixture bytes of " + p.id + "/" + file)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ortFixture holds a NuGet-layout package and the pin file that matches it.
type ortFixture struct {
	dir     string
	nupkg   string
	pins    string
	pkgHash string
}

func newORTFixture(t *testing.T) *ortFixture {
	t.Helper()
	dir := t.TempDir()
	f := &ortFixture{dir: dir, nupkg: filepath.Join(dir, "package.nupkg"), pins: filepath.Join(dir, "pins.sha256")}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range ortPlatforms {
		if p.nugetRID == "" {
			continue
		}
		for _, file := range p.files {
			w, err := zw.Create("runtimes/" + p.nugetRID + "/native/" + file)
			require.NoError(t, err)
			_, err = w.Write(p.content(file))
			require.NoError(t, err)
		}
	}
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(f.nupkg, buf.Bytes(), 0o600))
	f.pkgHash = hashHex(buf.Bytes())

	f.writePins(t, []string{f.pkgHash + "  microsoft.ml.onnxruntime." + fixtureVersion + ".nupkg"}, nil)
	return f
}

// writePins writes the pin file: the package line, then a pin per library file
// of every platform, except for the labels in override, whose hash is replaced.
func (f *ortFixture) writePins(t *testing.T, head []string, override map[string]string) {
	t.Helper()
	lines := slices.Clone(head)
	for _, p := range ortPlatforms {
		for _, file := range p.files {
			label := fixtureVersion + "/" + p.id + "/" + file
			h := hashHex(p.content(file))
			if o, ok := override[label]; ok {
				h = o
			}
			lines = append(lines, h+"  "+label)
		}
	}
	data := "# fixture pins\n" + strings.Join(lines, "\n") + "\n"
	require.NoError(t, os.WriteFile(f.pins, []byte(data), 0o600))
}

// githubArchive writes the release archive of p in the GitHub layout, with a
// "./" member prefix when dotSlash is set, and returns its path. tamper replaces
// the bytes of the library with that file name.
func (f *ortFixture) githubArchive(t *testing.T, p *ortPlatform, dotSlash bool, tamper string) string {
	t.Helper()
	asset := sprintVersion(p.asset)
	path := filepath.Join(f.dir, asset)
	root := strings.TrimSuffix(strings.TrimSuffix(asset, ".tgz"), ".zip")
	body := func(file string) []byte {
		if file == tamper {
			return []byte("tampered")
		}
		return p.content(file)
	}

	var buf bytes.Buffer
	if strings.HasSuffix(asset, ".zip") {
		zw := zip.NewWriter(&buf)
		for i, file := range p.files {
			w, err := zw.Create(root + "/lib/" + sprintVersion(p.installed[i]))
			require.NoError(t, err)
			_, err = w.Write(body(file))
			require.NoError(t, err)
		}
		require.NoError(t, zw.Close())
	} else {
		prefix := ""
		if dotSlash {
			prefix = "./"
		}
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for i, file := range p.files {
			data := body(file)
			require.NoError(t, tw.WriteHeader(&tar.Header{
				Name: prefix + root + "/lib/" + sprintVersion(p.installed[i]), Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg,
			}))
			_, err := tw.Write(data)
			require.NoError(t, err)
		}
		// A real archive also carries the links.
		for link, target := range p.links {
			require.NoError(t, tw.WriteHeader(&tar.Header{
				Name: prefix + root + "/lib/" + link, Linkname: sprintVersion(target), Typeflag: tar.TypeSymlink, Mode: 0o777,
			}))
		}
		require.NoError(t, tw.Close())
		require.NoError(t, gz.Close())
	}
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

// fetchRun is one run of the script. Empty fields leave the setting unset.
type fetchRun struct {
	source  string
	archive string
	pins    string
}

// runFetch runs the script and returns its stdout, stderr and error.
func runFetch(t *testing.T, r fetchRun, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sh", append([]string{fetchScriptPath}, args...)...)
	cmd.Env = append(os.Environ(),
		"ONNXRUNTIME_SOURCE="+r.source, "ONNXRUNTIME_ARCHIVE="+r.archive, "ONNXRUNTIME_PINS="+r.pins)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

// assertLayout checks that dest holds exactly the installed names of p: the
// versioned files with the fixture bytes and mode 755, and the links.
func assertLayout(t *testing.T, p *ortPlatform, dest string) {
	t.Helper()
	assert.Equal(t, p.installedNames(), entryNames(t, dest))
	for i, file := range p.files {
		path := filepath.Join(dest, sprintVersion(p.installed[i]))
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, p.content(file), got, "bytes of %s", path)
		info, err := os.Lstat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "mode of %s", path)
	}
	for link, target := range p.links {
		got, err := os.Readlink(filepath.Join(dest, link))
		require.NoError(t, err, "link %s", link)
		assert.Equal(t, sprintVersion(target), got, "target of %s", link)
	}
}

func platformByID(t *testing.T, id string) *ortPlatform {
	t.Helper()
	for _, p := range ortPlatforms {
		if p.id == id {
			return p
		}
	}
	require.FailNow(t, "unknown platform", id)
	return nil
}

// TestORTFetchScript runs the real install script against fixture archives
// and fixture pins: layout per platform, no install of unverified bytes, repair
// of a restored directory, the disabled github source and the pin generator.
func TestORTFetchScript(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("the script needs a POSIX shell")
	}
	for _, tool := range []string{"sh", "unzip", "tar"} {
		_, err := exec.LookPath(tool)
		require.NoError(t, err, "%s is required", tool)
	}
	// A cached test result must not outlive a change of the script or the pins:
	// Go keys the cache on files the test process opens, not on what the
	// subprocess reads (TESTING.md, "Go test cache in CI").
	for _, path := range []string{fetchScriptPath, pinFilePath} {
		_, err := os.ReadFile(path)
		require.NoError(t, err)
	}

	t.Run("nuget install lays out each platform", func(t *testing.T) {
		t.Parallel()
		for _, p := range ortPlatforms {
			if p.nugetRID == "" {
				continue
			}
			t.Run(p.id, func(t *testing.T) {
				t.Parallel()
				f := newORTFixture(t)
				dest := filepath.Join(t.TempDir(), "lib")
				_, stderr, err := runFetch(t, fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins},
					"install", fixtureVersion, p.id, dest)
				require.NoError(t, err, stderr)
				assertLayout(t, p, dest)
				assert.Empty(t, entryNames(t, dest+".download"), "no staged file stays behind")
			})
		}
	})

	t.Run("a library that does not match its pin installs nothing", func(t *testing.T) {
		t.Parallel()
		f := newORTFixture(t)
		f.writePins(t, []string{f.pkgHash + "  microsoft.ml.onnxruntime." + fixtureVersion + ".nupkg"},
			map[string]string{fixtureVersion + "/linux-amd64/libonnxruntime_providers_shared.so": hashHex([]byte("other"))})
		dest := filepath.Join(t.TempDir(), "lib")
		_, stderr, err := runFetch(t, fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins},
			"install", fixtureVersion, "linux-amd64", dest)
		require.Error(t, err)
		assert.Contains(t, stderr, "SHA-256 mismatch for "+fixtureVersion+"/linux-amd64/libonnxruntime_providers_shared.so")
		assert.Empty(t, entryNames(t, dest), "the matching first library must not be installed either")
	})

	t.Run("the package hash is checked before the package is read", func(t *testing.T) {
		t.Parallel()
		f := newORTFixture(t)
		bogus := filepath.Join(t.TempDir(), "bogus.nupkg")
		require.NoError(t, os.WriteFile(bogus, []byte("not a zip file"), 0o600))
		_, stderr, err := runFetch(t, fetchRun{source: "nuget", archive: bogus, pins: f.pins},
			"install", fixtureVersion, "linux-amd64", filepath.Join(t.TempDir(), "lib"))
		require.Error(t, err)
		assert.Contains(t, stderr, "SHA-256 mismatch for microsoft.ml.onnxruntime."+fixtureVersion+".nupkg")
	})

	t.Run("a restored directory is verified and repaired on every run", func(t *testing.T) {
		t.Parallel()
		f := newORTFixture(t)
		p := platformByID(t, "linux-amd64")
		dest := filepath.Join(t.TempDir(), "lib")
		run := fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins}
		_, stderr, err := runFetch(t, run, "install", fixtureVersion, p.id, dest)
		require.NoError(t, err, stderr)

		// Verified without any archive.
		noArchive := fetchRun{source: "nuget", archive: filepath.Join(f.dir, "missing.nupkg"), pins: f.pins}
		_, stderr, err = runFetch(t, noArchive, "install", fixtureVersion, p.id, dest)
		require.NoError(t, err, stderr)
		assert.Contains(t, stderr, "already in")

		// A deleted link and a changed mode are repaired.
		require.NoError(t, os.Remove(filepath.Join(dest, "libonnxruntime.so")))
		versioned := filepath.Join(dest, "libonnxruntime.so."+fixtureVersion)
		require.NoError(t, os.Chmod(versioned, 0o600))
		_, stderr, err = runFetch(t, noArchive, "install", fixtureVersion, p.id, dest)
		require.NoError(t, err, stderr)
		assertLayout(t, p, dest)

		// A changed library fails without the archive and is replaced with it.
		require.NoError(t, os.WriteFile(versioned, []byte("changed"), 0o755))
		_, _, err = runFetch(t, noArchive, "install", fixtureVersion, p.id, dest)
		require.Error(t, err)
		_, stderr, err = runFetch(t, run, "install", fixtureVersion, p.id, dest)
		require.NoError(t, err, stderr)
		assertLayout(t, p, dest)
	})

	t.Run("a directory with other content is rejected and left alone", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name  string
			plant func(t *testing.T, dest string) string
		}{
			{"stray file", func(t *testing.T, dest string) string {
				t.Helper()
				path := filepath.Join(dest, "notes.txt")
				require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
				return path
			}},
			{"dot dot name", func(t *testing.T, dest string) string {
				t.Helper()
				path := filepath.Join(dest, "..hidden")
				require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
				return path
			}},
			{"directory at a link name", func(t *testing.T, dest string) string {
				t.Helper()
				path := filepath.Join(dest, "libonnxruntime.so")
				require.NoError(t, os.Mkdir(path, 0o750))
				return path
			}},
			{"directory at a library name", func(t *testing.T, dest string) string {
				t.Helper()
				path := filepath.Join(dest, "libonnxruntime.so."+fixtureVersion)
				require.NoError(t, os.Mkdir(path, 0o750))
				return path
			}},
			{"file of an older version", func(t *testing.T, dest string) string {
				t.Helper()
				path := filepath.Join(dest, "libonnxruntime.so.1.0.0")
				require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
				return path
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				f := newORTFixture(t)
				dest := filepath.Join(t.TempDir(), "lib")
				require.NoError(t, os.Mkdir(dest, 0o750))
				planted := tc.plant(t, dest)
				_, stderr, err := runFetch(t, fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins},
					"install", fixtureVersion, "linux-amd64", dest)
				require.Error(t, err)
				assert.Contains(t, stderr, filepath.Base(planted))
				assert.Equal(t, []string{filepath.Base(planted)}, entryNames(t, dest), "nothing is installed next to it")
			})
		}
	})

	t.Run("installed files without a pin for the version fail", func(t *testing.T) {
		t.Parallel()
		f := newORTFixture(t)
		dest := filepath.Join(t.TempDir(), "lib")
		run := fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins}
		_, stderr, err := runFetch(t, run, "install", fixtureVersion, "linux-amd64", dest)
		require.NoError(t, err, stderr)

		empty := filepath.Join(f.dir, "empty-pins.sha256")
		require.NoError(t, os.WriteFile(empty, []byte("# no pins\n"), 0o600))
		_, stderr, err = runFetch(t, fetchRun{source: "nuget", archive: f.nupkg, pins: empty},
			"install", fixtureVersion, "linux-amd64", dest)
		require.Error(t, err)
		assert.Contains(t, stderr, "no pin for")
	})

	t.Run("github source gives the same layout", func(t *testing.T) {
		t.Parallel()
		for _, p := range ortPlatforms {
			for _, dotSlash := range []bool{false, true} {
				if dotSlash && !strings.HasSuffix(p.asset, ".tgz") {
					continue
				}
				t.Run(fmt.Sprintf("%s dotslash=%v", p.id, dotSlash), func(t *testing.T) {
					t.Parallel()
					f := newORTFixture(t)
					archive := f.githubArchive(t, p, dotSlash, "")
					dest := filepath.Join(t.TempDir(), "lib")
					_, stderr, err := runFetch(t, fetchRun{source: "github", archive: archive, pins: f.pins},
						"install", fixtureVersion, p.id, dest)
					require.NoError(t, err, stderr)
					assertLayout(t, p, dest)
				})
			}
		}
	})

	t.Run("github source rejects a library that differs from its pin", func(t *testing.T) {
		t.Parallel()
		f := newORTFixture(t)
		p := platformByID(t, "darwin-arm64")
		archive := f.githubArchive(t, p, false, "libonnxruntime.dylib")
		dest := filepath.Join(t.TempDir(), "lib")
		_, stderr, err := runFetch(t, fetchRun{source: "github", archive: archive, pins: f.pins},
			"install", fixtureVersion, p.id, dest)
		require.Error(t, err)
		assert.Contains(t, stderr, "SHA-256 mismatch for "+fixtureVersion+"/darwin-arm64/libonnxruntime.dylib")
		assert.Contains(t, stderr, "pinned "+hashHex(p.content("libonnxruntime.dylib")))
		assert.Contains(t, stderr, "got    "+hashHex([]byte("tampered")))
		assert.Empty(t, entryNames(t, dest))
	})

	t.Run("rejected inputs", func(t *testing.T) {
		t.Parallel()
		f := newORTFixture(t)
		dup := filepath.Join(f.dir, "dup.sha256")
		line := f.pkgHash + "  microsoft.ml.onnxruntime." + fixtureVersion + ".nupkg\n"
		require.NoError(t, os.WriteFile(dup, []byte(line+line), 0o600))

		cases := []struct {
			name    string
			run     fetchRun
			version string
			plat    string
			want    string
		}{
			{"intel mac on nuget", fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins}, fixtureVersion, "darwin-amd64", "pins for " + fixtureVersion + "/darwin-amd64/"},
			{"unknown source", fetchRun{source: "mirror", archive: f.nupkg, pins: f.pins}, fixtureVersion, "linux-amd64", "must be nuget or github"},
			{"duplicate pin label", fetchRun{source: "nuget", archive: f.nupkg, pins: dup}, fixtureVersion, "linux-amd64", "duplicate"},
			{"version without dots", fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins}, "1", "linux-amd64", "VERSION must look like"},
			{"unknown platform", fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins}, fixtureVersion, "plan9-amd64", "unsupported platform"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dest := filepath.Join(t.TempDir(), "lib")
				_, stderr, err := runFetch(t, tc.run, "install", tc.version, tc.plat, dest)
				require.Error(t, err)
				assert.Contains(t, stderr, tc.want)
				_, statErr := os.Stat(dest)
				if statErr == nil {
					assert.Empty(t, entryNames(t, dest))
				}
			})
		}
	})

	t.Run("generated pins make every platform install", func(t *testing.T) {
		t.Parallel()
		f := newORTFixture(t)
		stdout, stderr, err := runFetch(t, fetchRun{source: "nuget", archive: f.nupkg, pins: f.pins},
			"pins", fixtureVersion, filepath.Join(t.TempDir(), "dl"))
		require.NoError(t, err, stderr)
		generated := filepath.Join(f.dir, "generated.sha256")
		require.NoError(t, os.WriteFile(generated, []byte(stdout), 0o600))
		for _, p := range ortPlatforms {
			if p.nugetRID == "" {
				continue
			}
			t.Run(p.id, func(t *testing.T) {
				dest := filepath.Join(t.TempDir(), "lib")
				_, stderr, err := runFetch(t, fetchRun{source: "nuget", archive: f.nupkg, pins: generated},
					"install", fixtureVersion, p.id, dest)
				require.NoError(t, err, stderr)
				assertLayout(t, p, dest)
			})
		}
	})
}
