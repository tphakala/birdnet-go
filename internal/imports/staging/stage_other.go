//go:build !linux

package staging

import "os"

// openNoFollow is a stub on non-Linux platforms. Import staging requires Linux.
func openNoFollow(_ string) (*os.File, error) { return nil, ErrUnsupportedPlatform }

// chownTo is a stub on non-Linux platforms. Import staging requires Linux.
func chownTo(_ string, _, _ int) error { return ErrUnsupportedPlatform }
