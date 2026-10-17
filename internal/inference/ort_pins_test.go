package inference

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedYalueSeries is the onnxruntime_go series minORTMinor was derived from;
// move both together.
const expectedYalueSeries = "v1.36."

var (
	taskfilePinRe   = regexp.MustCompile(`(?m)^\s*ONNXRUNTIME_VERSION:\s*'([^']+)'`)
	dockerfilePinRe = regexp.MustCompile(`(?m)^ARG ONNXRUNTIME_VERSION=(\S+)`)
	actionPinRe     = regexp.MustCompile(`(?s)onnxruntime-version:.*?default:\s*'([^']+)'`)
	goModBindingRe  = regexp.MustCompile(`(?m)github\.com/yalue/onnxruntime_go\s+(v\S+)`)
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

// TestORTPinsAndBindingAgreeAndMeetMinimum keeps the native ONNX Runtime pins,
// the minimum version gate and the Go binding series in lockstep.
func TestORTPinsAndBindingAgreeAndMeetMinimum(t *testing.T) {
	t.Parallel()

	taskfile := firstGroup(t, "../../Taskfile.yml", taskfilePinRe)
	dockerfile := firstGroup(t, "../../Dockerfile", dockerfilePinRe)
	action := firstGroup(t, "../../.github/actions/setup-onnxruntime/action.yml", actionPinRe)
	binding := firstGroup(t, "../../go.mod", goModBindingRe)

	assert.Equal(t, taskfile, dockerfile, "Taskfile and Dockerfile pins differ")
	assert.Equal(t, taskfile, action, "Taskfile and setup-onnxruntime action pins differ")

	meets, known := ortVersionMeetsMinimum(taskfile)
	assert.True(t, known, "pin %q must parse", taskfile)
	assert.True(t, meets, "pin %q must meet the minimum %s", taskfile, ORTMinimumVersion())

	assert.True(t, strings.HasPrefix(binding, expectedYalueSeries),
		"onnxruntime_go %s is outside the %s series the minimum was derived from", binding, expectedYalueSeries)
}
