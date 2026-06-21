//go:build !linux || !cgo

package mempolicy

// setArenaMax is a no-op where glibc malloc / cgo is unavailable (non-Linux,
// or cgo-disabled builds). Returning false signals the cap was not applied.
func setArenaMax(int) bool { return false }
