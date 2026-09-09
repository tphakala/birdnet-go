package classifier

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsLibraryAbsent verifies the discrimination between a merely-absent
// OpenVINO shared library (which should be logged at Debug during device
// planning) and a present-but-broken one (which stays a Warn). The dynamic
// loader reports a missing library only through its error text across the cgo
// boundary, so the message is the signal; os.ErrNotExist is honored too.
func TestIsLibraryAbsent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error is not absent", nil, false},
		{
			"linux dlopen missing file",
			fmt.Errorf("libopenvino_c.so: cannot open shared object file: No such file or directory"),
			true,
		},
		{
			"bare no such file or directory",
			fmt.Errorf("open /opt/openvino/libopenvino_c.so: no such file or directory"),
			true,
		},
		{
			"macos dyld image not found",
			fmt.Errorf("dlopen(libopenvino_c.dylib): image not found"),
			true,
		},
		{
			"windows loadlibrary missing dll",
			fmt.Errorf("openvino_c.dll: The specified module could not be found."),
			true,
		},
		{
			"wrapped os.ErrNotExist",
			fmt.Errorf("stat libopenvino_c.so: %w", os.ErrNotExist),
			true,
		},
		{
			"present but broken: missing symbol",
			fmt.Errorf("libopenvino_c.so: undefined symbol: ov_core_create"),
			false,
		},
		{
			"present but broken: wrong ELF class",
			fmt.Errorf("libopenvino_c.so: wrong ELF class: ELFCLASS32"),
			false,
		},
		{
			// A present-but-inaccessible library carries the "cannot open shared
			// object file" text too, but "permission denied" marks it broken, not
			// absent, so it must stay a Warn.
			"present but broken: permission denied",
			fmt.Errorf("libopenvino_c.so: cannot open shared object file: Permission denied"),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isLibraryAbsent(tt.err))
		})
	}
}
