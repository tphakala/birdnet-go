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

func TestPreflightDiskSpace_Insufficient(t *testing.T) {
	h := New(testCore(t), nil)
	h.freeBytesFn = func(string) (uint64, error) { return 100, nil }
	require.ErrorIs(t, h.preflightDiskSpace(t.TempDir(), 1000), ErrInsufficientSpace)
}

func TestPreflightDiskSpace_Sufficient(t *testing.T) {
	h := New(testCore(t), nil)
	h.freeBytesFn = func(string) (uint64, error) { return 10_000, nil }
	require.NoError(t, h.preflightDiskSpace(t.TempDir(), 1000))
}
