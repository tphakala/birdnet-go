package apitest

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// fatalRecorder records Fatalf instead of failing, so a test can watch
// PublishTestSettings refuse a call without failing itself. Every other method,
// including Setenv, goes to the embedded real test and runs Go's own checks.
type fatalRecorder struct {
	testing.TB
	msg string
}

// Fatalf records the failure and returns instead of ending the test.
func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.msg = fmt.Sprintf(format, args...)
}

// TestPublishTestSettingsRefusesParallelTest pins the publish guard: a parallel
// test, and a serial subtest of a parallel parent, must fail with a message
// naming WithoutSettingsPublish, and a refused call must leave the
// process-global snapshot untouched.
//
// t.Parallel and the parent-child nesting are the inputs under test.
func TestPublishTestSettingsRefusesParallelTest(t *testing.T) {
	t.Parallel()

	requirePublishRefused(t)

	t.Run("serial subtest of a parallel parent", func(t *testing.T) {
		requirePublishRefused(t)
	})
}

// requirePublishRefused publishes through a recorder built from t, which must be
// parallel or have a parallel ancestor, and asserts the refusal. Only a publish
// by this call could install the fresh sentinel pointer, so no other test can
// break the NotSame assertion.
func requirePublishRefused(t *testing.T) {
	t.Helper()

	rec := &fatalRecorder{TB: t}
	sentinel := NewValidTestSettings()

	PublishTestSettings(rec, sentinel)

	require.Contains(t, rec.msg, "apitest.WithoutSettingsPublish()", "PublishTestSettings must refuse a parallel test")
	assert.NotSame(t, sentinel, conf.GetSettings(), "a refused publish must leave the global snapshot untouched")
}
