package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

// TestSchemaUsesYAMLKeys verifies the schema uses yaml struct tag names (not json)
// and excludes runtime-only fields (yaml:"-").
func TestSchemaUsesYAMLKeys(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(repoRoot, "config.schema.json"))
	if os.IsNotExist(err) {
		t.Skip("config.schema.json not yet generated")
	}
	require.NoError(t, err)

	var schema struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(data, &schema))

	settings := schema.Defs["Settings"].Properties
	// yaml tag is "webserver", json tag is "webServer" — schema must use yaml
	assert.Contains(t, settings, "webserver", "schema should use yaml key 'webserver', not json key 'webServer'")
	assert.NotContains(t, settings, "webServer", "schema should not contain json key 'webServer'")

	// Runtime fields (yaml:"-") must be excluded
	assert.NotContains(t, settings, "version", "runtime field 'version' should be excluded")
	assert.NotContains(t, settings, "buildDate", "runtime field 'buildDate' should be excluded")
	assert.NotContains(t, settings, "systemId", "runtime field 'systemId' should be excluded")

	audio := schema.Defs["AudioSettings"].Properties
	assert.NotContains(t, audio, "ffmpegVersion", "runtime field 'ffmpegVersion' should be excluded")
	assert.Contains(t, audio, "ffmpegpath", "user-configurable field 'ffmpegpath' should be present")
}

// TestConfigYAMLValidates ensures internal/conf/config.yaml passes validation
// against config.schema.json. This catches drift between the schema and the
// reference config (case mismatches, deprecated fields, type errors).
func TestConfigYAMLValidates(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	schemaPath := filepath.Join(repoRoot, "config.schema.json")
	configPath := filepath.Join(repoRoot, "internal", "conf", "config.yaml")

	schemaFile, err := os.Open(schemaPath)
	if os.IsNotExist(err) {
		t.Skip("config.schema.json not yet generated; run 'task generate-schema'")
	}
	require.NoError(t, err)
	defer schemaFile.Close()

	schemaDoc, err := jsonschema.UnmarshalJSON(schemaFile)
	require.NoError(t, err, "parsing schema JSON")

	configData, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var config any
	require.NoError(t, yaml.Unmarshal(configData, &config))

	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("schema.json", schemaDoc))
	schema, err := compiler.Compile("schema.json")
	require.NoError(t, err, "compiling schema")

	err = schema.Validate(config)
	if err != nil {
		t.Fatalf("config.yaml does not validate against schema:\n%v", err)
	}
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
