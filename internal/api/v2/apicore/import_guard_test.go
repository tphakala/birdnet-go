package apicore

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// This guard locks in the acyclic dependency direction the api/v2 split is built
// on (epic design 2.2/2.7): domains depend on apicore and dto; the facade
// (package api at internal/api/v2) depends on apicore and every domain; apicore
// depends on NEITHER a domain NOR the facade, and apitest depends only on
// apicore and dto. Because go test compiles a separate binary per package, the
// only thing stopping apicore from growing a domain import over time is a
// mechanical check: if apicore ever imported a domain (or a broadcaster grew a
// domain-typed payload), the import graph would close a cycle and this test
// fails, pointing at the offending package by name.
//
// The check shells out to `go list -deps`, which reports the transitive
// NON-TEST import set of a package. Non-test scope is exactly right: the rule is
// about the production build graph (a test file importing a domain cannot form a
// production cycle). No build tags are passed: build tags select files, so a
// tag-gated file could in principle add an import the default build never sees,
// but apicore and apitest currently have no build-constrained files, so the
// default build covers their entire import graph (narrow the check to a tag set
// if that ever changes). The test runs inside the normal `go test ./...`
// unit-test jobs on every platform, so it gates CI without a dedicated workflow
// step.

const (
	// apiV2Prefix is the import path of the api/v2 facade package (package api).
	apiV2Prefix = "github.com/tphakala/birdnet-go/internal/api/v2"
	// apicorePkg, apitestPkg and dtoPkg are the api/v2 leaf/substrate packages a
	// substrate package is allowed to import.
	apicorePkg = apiV2Prefix + "/apicore"
	apitestPkg = apiV2Prefix + "/apitest"
	dtoPkg     = apiV2Prefix + "/dto"
)

// listDeps returns the transitive non-test import paths of pkg via `go list
// -deps`. It skips (rather than fails) the test when the Go toolchain is not on
// PATH so the suite stays runnable in a stripped-down environment; a present-but-
// failing toolchain is a real problem and fails the test with the stderr output.
func listDeps(t *testing.T, pkg string) []string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available, skipping import guard: %v", err)
	}
	// Output() captures stdout only (the dep list); on failure stderr is carried on
	// the *exec.ExitError, so diagnostics never leak into the parsed import set.
	out, err := exec.Command("go", "list", "-deps", pkg).Output() //nolint:gosec // fixed args, no user input
	if err != nil {
		var exitErr *exec.ExitError
		var stderr []byte
		if errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}
		require.NoErrorf(t, err, "go list -deps %s failed: %s", pkg, stderr)
	}
	return strings.Fields(string(out))
}

// isAPIV2Package reports whether dep is the api/v2 facade or one of its
// subpackages. The facade is matched by exact equality and subpackages by the
// "/"-separated prefix, so a sibling like internal/api/v2foo can never match.
func isAPIV2Package(dep string) bool {
	return dep == apiV2Prefix || strings.HasPrefix(dep, apiV2Prefix+"/")
}

// assertNoForbiddenAPIV2Imports fails when pkg transitively imports any api/v2
// package outside allowed. allowed must list every api/v2 package the substrate
// package is permitted to depend on (itself plus the leaf packages); any other
// api/v2 dependency is a domain handler or the facade and closes a cycle.
func assertNoForbiddenAPIV2Imports(t *testing.T, pkg string, allowed map[string]bool) {
	t.Helper()
	for _, dep := range listDeps(t, pkg) {
		if !isAPIV2Package(dep) || allowed[dep] {
			continue
		}
		assert.Failf(t, "forbidden api/v2 import",
			"%s must not import %s: it would create a dependency cycle (the api/v2 split requires apicore/apitest to depend only on leaf packages, never on a domain or the facade)",
			pkg, dep)
	}
}

// TestApicoreDoesNotImportDomainsOrFacade asserts the substrate package apicore
// imports no api/v2 domain and not the facade. apicore may import only itself and
// the dto leaf package.
func TestApicoreDoesNotImportDomainsOrFacade(t *testing.T) {
	t.Parallel()
	assertNoForbiddenAPIV2Imports(t, apicorePkg, map[string]bool{
		apicorePkg: true,
		dtoPkg:     true,
	})
}

// TestApitestDoesNotImportDomainsOrFacade asserts the importable test-helper
// package apitest imports no api/v2 domain and not the facade. apitest may import
// only itself, apicore and the dto leaf package, so a domain test depending on
// apitest cannot pull in another domain (a domain -> apitest -> domain cycle).
func TestApitestDoesNotImportDomainsOrFacade(t *testing.T) {
	t.Parallel()
	assertNoForbiddenAPIV2Imports(t, apitestPkg, map[string]bool{
		apicorePkg: true,
		apitestPkg: true,
		dtoPkg:     true,
	})
}
