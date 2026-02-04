//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// SetFinalizerDeprecated detects runtime.SetFinalizer and suggests runtime.AddCleanup.
//
// The old pattern:
//
//	runtime.SetFinalizer(obj, func(o *Type) { cleanup(o) })
//
// New pattern (Go 1.24+):
//
//	runtime.AddCleanup(obj, func(arg ArgType) { cleanup(arg) }, arg)
//
// Benefits of AddCleanup:
//   - Multiple cleanups per object
//   - Can attach to interior pointers
//   - No cycle leaks (SetFinalizer can leak cycles)
//   - Doesn't delay object freeing
//   - Cleaner API with explicit cleanup argument
//
// See: https://pkg.go.dev/runtime#AddCleanup
func SetFinalizerDeprecated(m dsl.Matcher) {
	m.Match(
		`runtime.SetFinalizer($obj, $fn)`,
	).
		Report("consider using runtime.AddCleanup instead of runtime.SetFinalizer (Go 1.24+): AddCleanup allows multiple cleanups, avoids cycle leaks, and doesn't delay object freeing")
}

// GorootDeprecated detects runtime.GOROOT() which is deprecated in Go 1.24.
//
// The old pattern:
//
//	root := runtime.GOROOT()
//
// New pattern:
//
//	// Use go env GOROOT from command line or exec
//	cmd := exec.Command("go", "env", "GOROOT")
//	output, _ := cmd.Output()
//
// Reason: runtime.GOROOT() may not reflect the actual GOROOT when the binary
// is moved or when using toolchains.
//
// See: https://go.dev/doc/go1.24#runtime
func GorootDeprecated(m dsl.Matcher) {
	m.Match(
		`runtime.GOROOT()`,
	).
		Report("runtime.GOROOT() is deprecated in Go 1.24; use 'go env GOROOT' instead")
}
