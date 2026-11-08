package onnx

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakeEnv is an in-memory environment that records every set call.
type fakeEnv struct {
	vars    map[string]string
	setCall int
	setErr  error
}

func newFakeEnv() *fakeEnv { return &fakeEnv{vars: map[string]string{}} }

func (f *fakeEnv) lookup(key string) (string, bool) {
	v, ok := f.vars[key]
	return v, ok
}

func (f *fakeEnv) set(key, value string) error {
	f.setCall++
	if f.setErr != nil {
		return f.setErr
	}
	f.vars[key] = value
	return nil
}

func TestApplyTelemetryOptOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		initial      *string
		wantSet      bool
		wantDisabled bool
		wantValue    string
	}{
		{name: "unset", initial: nil, wantSet: true, wantDisabled: true, wantValue: "1"},
		{name: "empty", initial: new(""), wantSet: true, wantDisabled: true, wantValue: "1"},
		{name: "blank", initial: new("  "), wantSet: true, wantDisabled: true, wantValue: "1"},
		{name: "one", initial: new("1"), wantDisabled: true, wantValue: "1"},
		{name: "TRUE", initial: new("TRUE"), wantDisabled: true, wantValue: "TRUE"},
		{name: "padded yes", initial: new(" yes "), wantDisabled: true, wantValue: " yes "},
		{name: "on", initial: new("on"), wantDisabled: true, wantValue: "on"},
		{name: "y", initial: new("y"), wantDisabled: true, wantValue: "y"},
		{name: "zero", initial: new("0"), wantValue: "0"},
		{name: "false", initial: new("false"), wantValue: "false"},
		{name: "off", initial: new("off"), wantValue: "off"},
		{name: "no", initial: new("no"), wantValue: "no"},
		{name: "two", initial: new("2"), wantValue: "2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newFakeEnv()
			if tt.initial != nil {
				env.vars[ortDisableTelemetryEnv] = *tt.initial
			}
			got, err := applyTelemetryOptOut(env.lookup, env.set)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDisabled, got.Disabled)
			assert.Equal(t, tt.wantValue, got.Value)
			if tt.wantSet {
				assert.Equal(t, 1, env.setCall)
				assert.Equal(t, ortDisableTelemetryValue, env.vars[ortDisableTelemetryEnv])
			} else {
				assert.Zero(t, env.setCall, "a user value must not be overwritten")
			}
		})
	}

	t.Run("set error", func(t *testing.T) {
		t.Parallel()
		env := newFakeEnv()
		env.setErr = errors.NewStd("setenv failed")
		got, err := applyTelemetryOptOut(env.lookup, env.set)
		require.Error(t, err)
		assert.False(t, got.Disabled)
	})
}

func TestApplyTelemetryOptOut_Transitions(t *testing.T) {
	t.Parallel()

	type step struct {
		userSets  *string // applied to the environment before apply, when non-nil
		wantValue string
		wantSets  int // cumulative set calls after this step
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{name: "unset, apply, apply", steps: []step{
			{wantValue: "1", wantSets: 1},
			{wantValue: "1", wantSets: 1},
		}},
		{name: "user sets 0, apply, apply", steps: []step{
			{userSets: new("0"), wantValue: "0", wantSets: 0},
			{wantValue: "0", wantSets: 0},
		}},
		{name: "unset, apply, user sets 0, apply", steps: []step{
			{wantValue: "1", wantSets: 1},
			{userSets: new("0"), wantValue: "0", wantSets: 1},
		}},
		{name: "user sets blank, apply, apply", steps: []step{
			{userSets: new(" "), wantValue: "1", wantSets: 1},
			{wantValue: "1", wantSets: 1},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newFakeEnv()
			for i, st := range tt.steps {
				if st.userSets != nil {
					env.vars[ortDisableTelemetryEnv] = *st.userSets
				}
				got, err := applyTelemetryOptOut(env.lookup, env.set)
				require.NoError(t, err, "step %d", i)
				assert.Equal(t, st.wantValue, got.Value, "step %d value", i)
				assert.Equal(t, st.wantValue, env.vars[ortDisableTelemetryEnv], "step %d stored", i)
				assert.Equal(t, st.wantSets, env.setCall, "step %d set calls", i)
			}
		})
	}
}

func TestEnsureTelemetryOptOut_RealEnvironment(t *testing.T) {
	// Not parallel: mutates the process environment.
	t.Setenv(ortDisableTelemetryEnv, "0")
	got, err := ensureTelemetryOptOut()
	require.NoError(t, err)
	assert.False(t, got.Disabled)
	assert.Equal(t, "0", os.Getenv(ortDisableTelemetryEnv), "a user value must survive")

	require.NoError(t, os.Unsetenv(ortDisableTelemetryEnv))
	got, err = ensureTelemetryOptOut()
	require.NoError(t, err)
	assert.True(t, got.Disabled)
	assert.Equal(t, "1", os.Getenv(ortDisableTelemetryEnv))
}

func TestPackageInitSetsTelemetryOptOut(t *testing.T) {
	// Not parallel and no Setenv: observes what the package init() left behind.
	v, ok := os.LookupEnv(ortDisableTelemetryEnv)
	require.True(t, ok, "package init must set the variable")
	assert.NotEmpty(t, v)
}

func TestOrtTelemetryValueDisables(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"1", "true", "TRUE", "True", "yes", "YES", "on", "On", "y", "Y", " 1 ", "\ttrue\n"} {
		assert.True(t, ortTelemetryValueDisables(v), "%q should disable", v)
	}
	for _, v := range []string{"", " ", "0", "false", "no", "off", "n", "2", "enabled", "tru"} {
		assert.False(t, ortTelemetryValueDisables(v), "%q should not disable", v)
	}
}
