//go:build windows

package dbfailure

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/errors"
	"golang.org/x/sys/windows"
)

// TestClassify_WindowsErrors pins the Windows errors that stand in for the POSIX
// errnos classifyFile lists, and that a Windows error outside the mapped set stays
// KindUnknown. The error is wrapped the way ValidateStartupDiskSpace wraps
// the raw error from GetAvailableSpace.
func TestClassify_WindowsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		errno    error
		wantKind Kind
	}{
		{"parent is not a directory", windows.ERROR_DIRECTORY, KindSQLitePathUnusable},
		{"write protected volume", windows.ERROR_WRITE_PROTECT, KindSQLitePathUnusable},
		{"disk full", windows.ERROR_DISK_FULL, KindSQLiteDiskSpace},
		{"handle disk full", windows.ERROR_HANDLE_DISK_FULL, KindSQLiteDiskSpace},
		{"unrelated error stays unknown", windows.ERROR_INVALID_PARAMETER, KindUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := errors.New(tt.errno).
				Component("datastore").
				Category(errors.CategorySystem).
				Build()
			assert.Equal(t, tt.wantKind, Classify(err).Kind)
		})
	}
}
