package inference

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedBindingCommit is the commit suffix of the github.com/microsoft/onnxruntime/go
// pseudo-version that minORTMinor was last reviewed against (the ONNX Runtime
// v1.31.0 tag commit). Moving the binding means re-reading ORT_GO_API_VERSION_MIN
// in its cshim.h and this file together with minORTMinor.
const expectedBindingCommit = "402979ac1bef"

var (
	taskfilePinRe   = regexp.MustCompile(`(?m)^\s*ONNXRUNTIME_VERSION:\s*'([^']+)'`)
	dockerfilePinRe = regexp.MustCompile(`(?m)^ARG ONNXRUNTIME_VERSION=(\S+)`)
	actionPinRe     = regexp.MustCompile(`(?s)onnxruntime-version:.*?default:\s*'([^']+)'`)
	goModBindingRe  = regexp.MustCompile(`(?m)github\.com/microsoft/onnxruntime/go\s+(v\S+)`)
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

	assert.True(t, strings.HasSuffix(binding, "-"+expectedBindingCommit),
		"onnxruntime/go %s is not the commit %s the minimum was reviewed against", binding, expectedBindingCommit)
}
