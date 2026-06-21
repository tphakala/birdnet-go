package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchemaUpToDate verifies that config.schema.json is in sync with the
// source code. It regenerates the schema into a temp file and compares it
// against the committed version.
func TestSchemaUpToDate(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)

	committed, err := os.ReadFile(repoRoot + "/config.schema.json")
	if os.IsNotExist(err) {
		t.Skip("config.schema.json not yet generated; run 'task generate-schema'")
	}
	require.NoError(t, err, "reading committed schema")

	tmpFile := t.TempDir() + "/config.schema.json"
	cmd := exec.Command("go", "run", "./cmd/gen-schema/", tmpFile)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "regenerating schema: %s", out)

	regenerated, err := os.ReadFile(tmpFile)
	require.NoError(t, err, "reading regenerated schema")

	require.Equal(t, string(committed), string(regenerated),
		"config.schema.json is out of date; run 'task generate-schema' to update")
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "env", "GOMOD")
	out, err := cmd.Output()
	require.NoError(t, err, "finding go.mod")
	gomod := strings.TrimSpace(string(out))
	require.NotEmpty(t, gomod, "not inside a Go module")
	return gomod[:len(gomod)-len("/go.mod")]
}
