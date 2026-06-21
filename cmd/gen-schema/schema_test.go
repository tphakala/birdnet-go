package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchemaUpToDate verifies that config.schema.json and
// configuration-reference.md are in sync with the source code.
func TestSchemaUpToDate(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	tmpDir := t.TempDir()

	schemaPath := filepath.Join(repoRoot, "config.schema.json")
	mdPath := filepath.Join(repoRoot, "doc", "wiki", "configuration-reference.md")

	committedSchema, err := os.ReadFile(schemaPath)
	if os.IsNotExist(err) {
		t.Skip("config.schema.json not yet generated; run 'task generate-schema'")
	}
	require.NoError(t, err, "reading committed schema")

	committedMd, err := os.ReadFile(mdPath)
	if os.IsNotExist(err) {
		t.Skip("configuration-reference.md not yet generated; run 'task generate-schema'")
	}
	require.NoError(t, err, "reading committed markdown")

	tmpSchema := filepath.Join(tmpDir, "config.schema.json")
	tmpMd := filepath.Join(tmpDir, "configuration-reference.md")

	cmd := exec.Command("go", "run", "./cmd/gen-schema/", tmpSchema, tmpMd)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "regenerating: %s", out)

	regeneratedSchema, err := os.ReadFile(tmpSchema)
	require.NoError(t, err, "reading regenerated schema")

	regeneratedMd, err := os.ReadFile(tmpMd)
	require.NoError(t, err, "reading regenerated markdown")

	require.Equal(t, string(committedSchema), string(regeneratedSchema),
		"config.schema.json is out of date; run 'task generate-schema' to update")

	require.Equal(t, string(committedMd), string(regeneratedMd),
		"doc/wiki/configuration-reference.md is out of date; run 'task generate-schema' to update")
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
