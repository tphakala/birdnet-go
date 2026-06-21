// Command gen-schema generates a JSON Schema for BirdNET-Go's config.yaml.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gen-schema: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	r := &jsonschema.Reflector{
		AllowAdditionalProperties:  false,
		RequiredFromJSONSchemaTags: true,
		FieldNameTag:               "yaml",
		// Several fields use stdlib time.Duration directly (not conf.Duration),
		// which reflects as int64 nanoseconds. In YAML these are written as
		// strings like "1h" or "30s", so override the schema type to match.
		Mapper: func(t reflect.Type) *jsonschema.Schema {
			if t == reflect.TypeFor[time.Duration]() {
				return &jsonschema.Schema{
					Type:        "string",
					Description: "Duration string (e.g. \"30s\", \"5m\", \"1h\")",
					Examples:    []any{"10s", "5m", "1h"},
				}
			}
			return nil
		},
	}

	// AddGoComments parses Go source to extract struct field comments and
	// injects them as schema descriptions. Must be called before Reflect.
	for _, dir := range []string{"internal/conf", "internal/logger"} {
		if err := r.AddGoComments("github.com/tphakala/birdnet-go", filepath.Join(".", dir)); err != nil {
			return fmt.Errorf("adding comments from %s: %w", dir, err)
		}
	}

	schema := r.Reflect(&conf.Settings{})
	schema.ID = "https://raw.githubusercontent.com/tphakala/birdnet-go/main/config.schema.json"
	schema.Title = "BirdNET-Go Configuration"
	schema.Description = "Configuration schema for BirdNET-Go's config.yaml file."

	if schema.Definitions != nil {
		if settingsDef, ok := schema.Definitions["Settings"]; ok {
			settingsDef.Title = "BirdNET-Go Configuration"
			settingsDef.Description = "Top-level configuration for BirdNET-Go."
		}
	}

	out, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling schema: %w", err)
	}

	outPath := "config.schema.json"
	mdPath := filepath.Join("doc", "wiki", "configuration-reference.md")
	if len(os.Args) > 1 {
		outPath = os.Args[1]
	}
	if len(os.Args) > 2 {
		mdPath = os.Args[2]
	}

	if err := os.WriteFile(outPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", outPath, len(out))

	md := renderMarkdown(schema)
	if err := os.MkdirAll(filepath.Dir(mdPath), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", mdPath, err)
	}
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", mdPath, err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", mdPath, len(md))

	return nil
}
