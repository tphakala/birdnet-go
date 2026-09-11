package speciesindex

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// goListTimeout bounds the nested `go list` so the guard fails fast rather than
// hanging if the toolchain stalls. Resolving an already-built package's deps is
// sub-second, so this is generous headroom.
const goListTimeout = 60 * time.Second

// speciesindexPkg is the import path of the package under test.
const speciesindexPkg = "github.com/tphakala/birdnet-go/internal/speciesindex"

// forbiddenImports are the import groups speciesindex must never pull in, so it
// stays a leaf importable from classifier, api/v2 and analysis without a cycle.
// This is a denylist: it asserts the absence of these groups, not a positive
// allowlist of every permitted package.
var forbiddenImports = []struct {
	label string
	match func(dep string) bool
}{
	{"internal/classifier", func(d string) bool {
		return d == "github.com/tphakala/birdnet-go/internal/classifier" ||
			strings.HasPrefix(d, "github.com/tphakala/birdnet-go/internal/classifier/")
	}},
	{"internal/api", func(d string) bool {
		return d == "github.com/tphakala/birdnet-go/internal/api" ||
			strings.HasPrefix(d, "github.com/tphakala/birdnet-go/internal/api/")
	}},
	{"internal/analysis", func(d string) bool {
		return d == "github.com/tphakala/birdnet-go/internal/analysis" ||
			strings.HasPrefix(d, "github.com/tphakala/birdnet-go/internal/analysis/")
	}},
}

// listDeps returns the transitive non-test import paths of pkg via `go list
// -deps`. Non-test scope is exactly right: the rule is about the production build
// graph (a test importing a heavier package cannot form a production cycle). It
// skips when the toolchain is absent and fails on a present-but-failing one.
func listDeps(t *testing.T, pkg string) []string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available, skipping import guard: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), goListTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "list", "-deps", pkg).Output() //nolint:gosec // fixed args, no user input
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			require.Failf(t, "go list timed out",
				"go list -deps %s did not finish within %s", pkg, goListTimeout)
		}
		var exitErr *exec.ExitError
		var stderr []byte
		if errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}
		require.NoErrorf(t, err, "go list -deps %s failed: %s", pkg, stderr)
	}
	return strings.Fields(string(out))
}

// TestSpeciesindexImportsOnlyLeafPackages asserts speciesindex transitively
// imports no classifier, api, or analysis package, so it remains importable from
// all of them without closing a dependency cycle.
func TestSpeciesindexImportsOnlyLeafPackages(t *testing.T) {
	t.Parallel()

	for _, dep := range listDeps(t, speciesindexPkg) {
		for _, f := range forbiddenImports {
			assert.Falsef(t, f.match(dep),
				"speciesindex must not import %s (%s): it must stay a leaf package importable from classifier/api/analysis",
				dep, f.label)
		}
	}
}
