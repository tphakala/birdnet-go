package diagnostics

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// diagnosticsPkg is this package's import path.
const diagnosticsPkg = "github.com/tphakala/birdnet-go/internal/diagnostics"

// internalPrefix is the module's internal package namespace.
const internalPrefix = "github.com/tphakala/birdnet-go/internal/"

// forbiddenDeps are packages diagnostics must never depend on, directly
// or transitively. telemetry is listed even though a compile-time cycle
// would not exist: it is a deliberate design boundary (see package doc).
var forbiddenDeps = []string{
	internalPrefix + "telemetry",
	internalPrefix + "datastore",
	internalPrefix + "datastore/v2",
	internalPrefix + "support",
	internalPrefix + "analysis",
	internalPrefix + "app",
}

// allowedInternalDeps is the exact allowlisted internal closure of this
// package. Any widening means an import was added that could reintroduce
// deep-cycle risk; update this list only together with a Scope review in
// the boot-journal plan document.
var allowedInternalDeps = map[string]struct{}{
	diagnosticsPkg:                   {},
	internalPrefix + "conf":          {},
	internalPrefix + "errors":        {},
	internalPrefix + "logger":        {},
	internalPrefix + "privacy":       {},
	internalPrefix + "sysinfo":       {},
	internalPrefix + "csvutil":       {},
	internalPrefix + "openfauna":     {},
	internalPrefix + "templatefuncs": {},
}

// TestDiagnosticsImportGuard enforces two invariants: (a) diagnostics
// never depends on telemetry, datastore, support, analysis, or app;
// (b) its internal closure stays exactly the allowlisted set, so nothing
// capable of importing diagnostics can ever enter that closure.
func TestDiagnosticsImportGuard(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", diagnosticsPkg).Output()
	require.NoError(t, err, "go list -deps must succeed")

	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	depSet := make(map[string]struct{}, len(deps))
	for _, d := range deps {
		depSet[strings.TrimSpace(d)] = struct{}{}
	}

	for _, forbidden := range forbiddenDeps {
		_, found := depSet[forbidden]
		assert.False(t, found, "diagnostics must not depend on %s", forbidden)
	}

	for dep := range depSet {
		if !strings.HasPrefix(dep, internalPrefix) {
			continue
		}
		_, ok := allowedInternalDeps[dep]
		assert.True(t, ok, "unexpected internal package in diagnostics closure: %s", dep)
	}
}
