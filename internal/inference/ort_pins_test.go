package inference

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindingModulePath is the Go module whose C API level sets the minimum ONNX
// Runtime version.
const bindingModulePath = "github.com/yalue/onnxruntime_go"

var (
	taskfilePinRe   = regexp.MustCompile(`(?m)^\s*ONNXRUNTIME_VERSION:\s*'([^']+)'`)
	dockerfilePinRe = regexp.MustCompile(`(?m)^ARG ONNXRUNTIME_VERSION=(\S+)`)
	actionPinRe     = regexp.MustCompile(`(?s)onnxruntime-version:.*?default:\s*'([^']+)'`)
	apiVersionRe    = regexp.MustCompile(`(?m)^#define ORT_API_VERSION\s+(\d+)`)
	// docPinRe matches the shipped version the install guides state: the
	// VERSION= lines of the install snippets and the "ship X" sentences.
	docPinRe = regexp.MustCompile(`(?m)(?:^\$?VERSION\s*=\s*"?|\bship )(\d+\.\d+\.\d+)`)
	// docVersionAssignRe matches the VERSION assignment that opens each install
	// snippet of a guide.
	docVersionAssignRe = regexp.MustCompile(`(?m)^\$?VERSION\s*=`)
	sha256HexRe        = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	pinLineRe          = regexp.MustCompile(`^([0-9a-f]{64}) {2}(\S+)$`)
)

const (
	fetchScriptPath = "../../scripts/fetch-onnxruntime.sh"
	pinFilePath     = "../../scripts/onnxruntime-pins.sha256"
)

// installGuides are the user install guides that name the shipped ONNX Runtime
// version.
var installGuides = []string{
	"../../doc/ONNX-Runtime-Installation.md",
	"../../doc/wiki/onnx-runtime-installation.md",
}

// firstGroup returns the first capture group of re in the file at path.
func firstGroup(t *testing.T, path string, re *regexp.Regexp) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read pin file %s", path)
	m := re.FindSubmatch(data)
	require.NotNil(t, m, "no match for %s in %s", re, path)
	return string(m[1])
}

// TestORTPinsAgreeAndMeetMinimum keeps the native ONNX Runtime pins in step with
// each other and with the minimum version gate.
func TestORTPinsAgreeAndMeetMinimum(t *testing.T) {
	t.Parallel()

	taskfile := firstGroup(t, "../../Taskfile.yml", taskfilePinRe)
	dockerfile := firstGroup(t, "../../Dockerfile", dockerfilePinRe)
	action := firstGroup(t, "../../.github/actions/setup-onnxruntime/action.yml", actionPinRe)

	assert.Equal(t, taskfile, dockerfile, "Taskfile and Dockerfile pins differ")
	assert.Equal(t, taskfile, action, "Taskfile and setup-onnxruntime action pins differ")

	meets, known := ortVersionMeetsMinimum(taskfile)
	assert.True(t, known, "pin %q must parse", taskfile)
	assert.True(t, meets, "pin %q must meet the minimum %s", taskfile, ORTMinimumVersion())
}

// TestORTInstallGuidesMatchPin keeps the version the install guides tell users
// to download, and the package hash they tell them to check, in step with the
// native pins.
func TestORTInstallGuidesMatchPin(t *testing.T) {
	t.Parallel()

	pin := firstGroup(t, "../../Taskfile.yml", taskfilePinRe)
	pkgPin := readPins(t)[packageLabel(pin)]
	require.NotEmpty(t, pkgPin, "no package pin for %s", pin)
	for _, guide := range installGuides {
		data, err := os.ReadFile(guide)
		require.NoError(t, err, "read install guide %s", guide)
		matches := docPinRe.FindAllSubmatch(data, -1)
		require.NotEmpty(t, matches, "no shipped version found in %s", guide)
		for _, m := range matches {
			assert.Equal(t, pin, string(m[1]), "%s states %s, the Taskfile pins %s", guide, m[1], pin)
		}

		// Every snippet that downloads the package also checks it against the
		// package pin, so the hashes are as many as the snippets.
		hashes := sha256HexRe.FindAllString(string(data), -1)
		for _, h := range hashes {
			assert.Equal(t, pkgPin, h, "%s carries a hash that is not the package pin", guide)
		}
		assert.Len(t, hashes, len(docVersionAssignRe.FindAllString(string(data), -1)),
			"%s: every snippet with a VERSION line needs the package hash check", guide)

		// A failed check must stop the snippet: the lines that extract and install
		// the library only run inside the block the check opens.
		// The PowerShell check has no output of its own, so its else branch is the
		// only error a Windows user sees.
		assert.Equal(t, strings.Count(string(data), "Get-FileHash"),
			strings.Count(string(data), `throw "onnxruntime.nupkg does not match the expected SHA-256"`),
			"%s: every PowerShell check needs its throw", guide)
		for line := range strings.SplitSeq(string(data), "\n") {
			switch {
			case strings.Contains(line, " -c - "), strings.HasSuffix(line, " -c -"):
				assert.True(t, strings.HasSuffix(line, " -c - && {"), "%s: the check must open a guarded block: %s", guide, line)
			case strings.Contains(line, "Get-FileHash"):
				assert.True(t, strings.Contains(line, " -eq ") && strings.HasSuffix(line, ") {"), "%s: the check must open a guarded block: %s", guide, line)
			}
		}
	}
}

// packageLabel is the pin label of the NuGet package at version.
func packageLabel(version string) string {
	return "microsoft.ml.onnxruntime." + version + ".nupkg"
}

// readPins parses the pin file into label to hash, failing on a malformed line
// or a label pinned twice.
func readPins(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(pinFilePath)
	require.NoError(t, err, "read %s", pinFilePath)
	pins := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := pinLineRe.FindStringSubmatch(line)
		require.NotNil(t, m, "malformed pin line %q", line)
		_, dup := pins[m[2]]
		require.False(t, dup, "label %s pinned twice", m[2])
		pins[m[2]] = m[1]
	}
	return pins
}

// TestORTPinFileCoversPinnedVersion keeps the SHA-256 pin file complete for the
// pinned ONNX Runtime version: the package and every library file of every
// release platform, so a version bump without regenerated pins fails here and
// not in the next uncached release build.
func TestORTPinFileCoversPinnedVersion(t *testing.T) {
	t.Parallel()

	version := firstGroup(t, "../../Taskfile.yml", taskfilePinRe)
	pins := readPins(t)

	want := map[string]bool{packageLabel(version): true}
	for _, p := range ortPlatforms {
		if !p.release {
			continue
		}
		for _, f := range p.files {
			want[version+"/"+p.id+"/"+f] = true
		}
	}
	for label := range want {
		assert.Contains(t, pins, label, "pin file has no pin for %s", label)
	}
	// The github source also pins the Intel Mac library; nothing else belongs.
	for label := range pins {
		if want[label] {
			continue
		}
		assert.Equal(t, version+"/darwin-amd64/libonnxruntime.dylib", label, "unexpected pin label")
	}
}

// TestORTFetchScriptIsTheOnlyDownloader keeps every ONNX Runtime download behind
// scripts/fetch-onnxruntime.sh, the one place that holds the source switch and
// checks hashes.
func TestORTFetchScriptIsTheOnlyDownloader(t *testing.T) {
	t.Parallel()

	callers := []string{
		"../../Taskfile.yml",
		"../../Dockerfile",
		"../../.github/actions/setup-onnxruntime/action.yml",
		"../../.github/workflows/windows-test.yml",
	}
	for _, path := range callers {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(data), "fetch-onnxruntime.sh", "%s must install ONNX Runtime through the script", path)
	}

	workflows, err := filepath.Glob("../../.github/workflows/*.yml")
	require.NoError(t, err)
	actions, err := filepath.Glob("../../.github/actions/*/action.yml")
	require.NoError(t, err)
	require.NotEmpty(t, workflows)
	require.NotEmpty(t, actions)
	checked := append([]string{"../../Taskfile.yml", "../../Dockerfile"}, workflows...)
	checked = append(checked, actions...)
	forbidden := []string{"microsoft/onnxruntime/releases", "api.nuget.org", "ONNXRUNTIME_SOURCE"}
	for _, path := range checked {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, f := range forbidden {
			assert.NotContains(t, string(data), f, "%s must not carry its own download source", path)
		}
	}
}

// TestORTBaseImageTagHashesFetchInputs keeps the published base image tag
// content-addressed on every input of runtime-base, so a pin or source change
// rebuilds the base.
func TestORTBaseImageTagHashesFetchInputs(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../.github/workflows/base-image.yml")
	require.NoError(t, err)
	var tagLine string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.Contains(line, `TAG="base-`) {
			tagLine = line
		}
	}
	require.NotEmpty(t, tagLine, "base-image.yml has no TAG line")
	for _, input := range []string{"Dockerfile", "scripts/fetch-onnxruntime.sh", "scripts/onnxruntime-pins.sha256"} {
		assert.Contains(t, tagLine, input, "the base image tag must hash %s", input)
	}
}

// TestORTCIKeysTrackFetchInputs keeps the CI cache keys and path filters that
// depend on the ONNX Runtime download in step with the script and the pin file:
// a missing entry in any one list lets a pin or source change reuse a stale
// cache or skip a job.
func TestORTCIKeysTrackFetchInputs(t *testing.T) {
	t.Parallel()

	files := []string{"scripts/fetch-onnxruntime.sh", "scripts/onnxruntime-pins.sha256"}
	// pathLists is how many path lists of the workflow must hold each file as a
	// list entry; hashLine is a line that must name both files in a hashFiles call.
	cases := []struct {
		file      string
		pathLists int
		hashLine  string
	}{
		{"../../.github/actions/setup-onnxruntime/action.yml", 0, "key: onnxruntime-"},
		{"../../.github/workflows/golangci-test.yml", 2, "CI_FILES_HASH:"},
		{"../../.github/workflows/container-test.yml", 2, ""},
		{"../../.github/workflows/windows-test.yml", 1, ""},
		{"../../.github/workflows/macos-test.yml", 1, ""},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(tc.file)
		require.NoError(t, err)
		lines := strings.Split(string(data), "\n")
		for _, f := range files {
			entries := 0
			for _, line := range lines {
				entry := strings.Trim(strings.TrimPrefix(strings.TrimSpace(line), "- "), `'"`)
				if strings.HasPrefix(strings.TrimSpace(line), "- ") && entry == f {
					entries++
				}
			}
			assert.Equal(t, tc.pathLists, entries, "%s: path list entries for %s", tc.file, f)
		}
		if tc.hashLine == "" {
			continue
		}
		var found bool
		for _, line := range lines {
			if !strings.Contains(line, tc.hashLine) {
				continue
			}
			found = true
			for _, f := range files {
				assert.Contains(t, line, f, "%s: %q must hash %s", tc.file, tc.hashLine, f)
			}
		}
		assert.True(t, found, "%s: no line with %q", tc.file, tc.hashLine)
	}
}

// TestORTMinimumMatchesBindingAPIVersion ties the minimum version gate to the C
// API level the onnxruntime_go binding requests: ONNX Runtime 1.N serves API N and
// older, so minORTMinor must equal ORT_API_VERSION in the binding's header.
func TestORTMinimumMatchesBindingAPIVersion(t *testing.T) {
	t.Parallel()

	out, err := exec.CommandContext(t.Context(), "go", "list", "-f", "{{.Dir}}", bindingModulePath).Output()
	require.NoError(t, err, "locate %s", bindingModulePath)
	header := filepath.Join(strings.TrimSpace(string(out)), "onnxruntime_c_api.h")

	assert.Equal(t, strconv.Itoa(minORTMinor), firstGroup(t, header, apiVersionRe),
		"minORTMinor must equal the C API level %s requests; move it with the binding", bindingModulePath)
}
