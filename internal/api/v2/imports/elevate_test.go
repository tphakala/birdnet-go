//go:build linux

// elevate_test.go: linux-gated tests for trusted-base verification, staging-dst
// generation, and disk-space preflight helpers.
package importsapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssertTrustedBase_RejectsServiceOwnedDir(t *testing.T) {
	// A t.TempDir() is owned by the test (service) user and not sticky, exactly
	// the kind of base we must refuse (a local user can rename or replace it).
	require.Error(t, assertTrustedBase(t.TempDir()))
}

func TestAssertTrustedBase_AcceptsVarTmp(t *testing.T) {
	// /var/tmp is root-owned + sticky on a normal Linux system. Skip if the CI
	// runner is unusual.
	if err := assertTrustedBase("/var/tmp"); err != nil {
		t.Skipf("/var/tmp is not a trusted base on this runner: %v", err)
	}
}

func TestNewStagingDst_ReturnsNonExistentChildUnderBase(t *testing.T) {
	base := filepath.Join(t.TempDir(), "stg")
	require.NoError(t, os.MkdirAll(base, 0o700))

	h := New(testCore(t), nil)
	h.verifyTrustedBase = func(string) error { return nil } // bypass: tests cannot make a root dir
	dst, err := h.newStagingDst(base)
	require.NoError(t, err)
	assert.True(t, isStrictlyUnder(base, dst))
	assert.NoDirExists(t, dst, "dst must not pre-exist; import-stage creates it")
}

func TestNewStagingDst_RefusesUntrustedBase(t *testing.T) {
	h := New(testCore(t), nil)
	h.verifyTrustedBase = func(string) error { return ErrStagingBaseUnavailable }
	_, err := h.newStagingDst(t.TempDir())
	require.ErrorIs(t, err, ErrStagingBaseUnavailable)
}

// TestPreflightDiskSpace verifies the staging disk check: one byte short fails,
// exactly enough passes, and a probe error is passed through unchanged.
func TestPreflightDiskSpace(t *testing.T) {
	const required = 1000
	tests := []struct {
		name    string
		free    uint64
		freeErr error
		wantErr error
	}{
		{name: "one byte short", free: required - 1, wantErr: ErrInsufficientSpace},
		{name: "exactly enough", free: required},
		{name: "probe error is passed through", freeErr: ErrDiskCheckUnsupported, wantErr: ErrDiskCheckUnsupported},
	}

	h := New(testCore(t), nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.preflightDiskSpace(tt.free, tt.freeErr, required)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
