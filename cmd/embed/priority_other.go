//go:build !linux

package embed

// lowerPriority is a no-op on platforms without Unix scheduling priorities
// wired here. Operators can use OS-level tooling instead.
func lowerPriority() {}
