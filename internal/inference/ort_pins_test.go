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
)

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
