//go:build !linux

package importsapi

// freeBytes is unsupported off Linux; native import staging is Linux-only.
func freeBytes(_ string) (uint64, error) { return 0, ErrDiskCheckUnsupported }

// assertTrustedBase is unsupported off Linux; native elevation staging is
// Linux-only, so a non-Linux caller falls back to copy-paste remediation.
func assertTrustedBase(_ string) error { return ErrStagingBaseUnavailable }
