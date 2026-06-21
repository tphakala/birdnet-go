// Command gen-schema generates a JSON Schema for BirdNET-Go's config.yaml.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"

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
	comments, err := extractComments()
	if err != nil {
		return fmt.Errorf("extracting comments: %w", err)
	}

	r := &jsonschema.Reflector{
		DoNotReference:             false,
		ExpandedStruct:             false,
		AllowAdditionalProperties:  true,
		RequiredFromJSONSchemaTags: true,
	}

	schema := r.Reflect(&conf.Settings{})
	schema.ID = "https://raw.githubusercontent.com/tphakala/birdnet-go/main/config.schema.json"
	schema.Title = "BirdNET-Go Configuration"
	schema.Description = "Configuration schema for BirdNET-Go's config.yaml file."

	applyComments(schema, comments)

	// Enrich the Settings $def with title/description as well.
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

// commentMap maps "StructName.FieldName" to extracted doc/line comments.
type commentMap map[string]string

func extractComments() (commentMap, error) {
	cm := make(commentMap)
	fset := token.NewFileSet()

	for _, dir := range []string{
		filepath.Join("internal", "conf"),
		filepath.Join("internal", "logger"),
	} {
		if err := parseDir(fset, dir, cm); err != nil {
			return nil, err
		}
	}

	return cm, nil
}

func parseDir(fset *token.FileSet, dir string, cm commentMap) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		extractFileComments(file, cm)
	}

	return nil
}

func extractFileComments(file *ast.File, cm commentMap) {
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		structName := ts.Name.Name
		for _, field := range st.Fields.List {
			if len(field.Names) == 0 {
				continue
			}
			fieldName := field.Names[0].Name
			comment := extractFieldComment(field)
			if comment != "" {
				cm[structName+"."+fieldName] = comment
			}
		}
		return true
	})
}

func extractFieldComment(field *ast.Field) string {
	if field.Comment != nil {
		return cleanComment(field.Comment.Text())
	}
	if field.Doc != nil {
		return cleanComment(field.Doc.Text())
	}
	return ""
}

func cleanComment(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "//")
	s = strings.TrimSpace(s)
	return s
}

// applyComments walks the schema and enriches definitions with extracted comments.
func applyComments(schema *jsonschema.Schema, cm commentMap) {
	visited := make(map[*jsonschema.Schema]bool)
	applyToSchema(schema, reflect.TypeFor[conf.Settings](), cm, visited)

	if schema.Definitions != nil {
		for defName, defSchema := range schema.Definitions {
			t := findTypeByName(defName)
			if t != nil {
				applyToSchema(defSchema, t, cm, visited)
			}
		}
	}
}

func applyToSchema(schema *jsonschema.Schema, t reflect.Type, cm commentMap, visited map[*jsonschema.Schema]bool) {
	if schema == nil || visited[schema] {
		return
	}
	visited[schema] = true

	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return
	}

	structName := t.Name()
	if structName == "" {
		return
	}

	if schema.Properties == nil {
		return
	}

	for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
		jsonKey := pair.Key
		propSchema := pair.Value

		field, ok := findFieldByJSONKey(t, jsonKey)
		if !ok {
			continue
		}

		key := structName + "." + field.Name
		if desc, found := cm[key]; found && propSchema.Description == "" {
			propSchema.Description = desc
		}

		fieldType := field.Type
		for fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct {
			applyToSchema(propSchema, fieldType, cm, visited)
		}
	}
}

func findFieldByJSONKey(t reflect.Type, jsonKey string) (reflect.StructField, bool) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return reflect.StructField{}, false
	}

	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == jsonKey {
			return f, true
		}
		if f.Anonymous {
			if sf, ok := findFieldByJSONKey(f.Type, jsonKey); ok {
				return sf, true
			}
		}
	}
	return reflect.StructField{}, false
}

// findTypeByName maps a schema definition name back to its reflect.Type.
// We build the mapping by walking the Settings struct tree.
func findTypeByName(name string) reflect.Type {
	typeMap := buildTypeMap()
	return typeMap[name]
}

var cachedTypeMap map[string]reflect.Type

func buildTypeMap() map[string]reflect.Type {
	if cachedTypeMap != nil {
		return cachedTypeMap
	}
	cachedTypeMap = make(map[string]reflect.Type)
	visited := make(map[reflect.Type]bool)
	collectTypes(reflect.TypeFor[conf.Settings](), visited, cachedTypeMap)
	return cachedTypeMap
}

func collectTypes(t reflect.Type, visited map[reflect.Type]bool, m map[string]reflect.Type) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		collectTypes(t.Elem(), visited, m)
		return
	}

	if t.Kind() != reflect.Struct {
		return
	}

	if visited[t] {
		return
	}
	visited[t] = true

	if t.Name() != "" {
		m[t.Name()] = t
	}

	for f := range t.Fields() {
		collectTypes(f.Type, visited, m)
	}
}
