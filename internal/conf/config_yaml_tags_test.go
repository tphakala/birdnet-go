package conf

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "gopkg.in/yaml.v3" // imported for round-trip test added later
)

const projectModulePath = "github.com/tphakala/birdnet-go"

// TestAllSettingsStructsHaveYAMLTags verifies that every exported field in the
// Settings struct tree that has a json: tag also has an explicit yaml: tag.
// Without explicit yaml: tags, gopkg.in/yaml.v3 lowercases the Go field name
// (e.g., MaxAge → maxage), creating a fragile config format.
func TestAllSettingsStructsHaveYAMLTags(t *testing.T) {
	t.Parallel()

	var missing []string
	visited := make(map[reflect.Type]bool)

	checkType(reflect.TypeFor[Settings](), "Settings", visited, &missing)

	// Report all missing fields first for easy diagnosis
	for _, m := range missing {
		t.Logf("missing yaml tag: %s", m)
	}

	require.Empty(t, missing,
		"found %d exported fields with json: tag but no yaml: tag;\n"+
			"add explicit yaml: tags to fix config serialization correctness",
		len(missing))

	assert.Empty(t, missing) // Belt-and-suspenders; require above will short-circuit
}

// checkType recursively walks a struct type and collects fields that have a
// json: tag but no yaml: tag.
func checkType(t reflect.Type, path string, visited map[reflect.Type]bool, missing *[]string) {
	// Dereference pointer types
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return
	}

	// Avoid infinite recursion on recursive types
	if visited[t] {
		return
	}
	visited[t] = true

	// Only recurse into project-owned types or anonymous (inline) structs.
	// Inline structs have an empty PkgPath; named types from stdlib or
	// third-party packages will have a non-project PkgPath.
	if t.PkgPath() != "" && !strings.HasPrefix(t.PkgPath(), projectModulePath) {
		return
	}

	for field := range t.Fields() {
		f := field // capture

		if !f.IsExported() {
			continue
		}

		fieldPath := fmt.Sprintf("%s.%s", path, f.Name)

		jsonTag := f.Tag.Get("json")
		yamlTag := f.Tag.Get("yaml")

		// Skip fields explicitly excluded from JSON
		if jsonTag == "-" || jsonTag == "" {
			// If json is absent or "-", no yaml tag is required for config
			// serialization purposes — but still recurse into struct fields.
			recurseInto(f.Type, fieldPath, visited, missing)
			continue
		}

		// Skip fields excluded from YAML (intentionally runtime-only)
		if yamlTag == "-" {
			continue
		}

		// If field has a json tag but no yaml tag, record it
		if yamlTag == "" {
			*missing = append(*missing, fieldPath)
		}

		recurseInto(f.Type, fieldPath, visited, missing)
	}
}

// recurseInto resolves the element type for pointers, slices, and maps, then
// calls checkType so we validate nested structs.
func recurseInto(ft reflect.Type, path string, visited map[reflect.Type]bool, missing *[]string) {
	// Unwrap pointers
	for ft.Kind() == reflect.Ptr {
		ft = ft.Elem()
	}

	switch ft.Kind() {
	case reflect.Struct:
		checkType(ft, path, visited, missing)
	case reflect.Slice:
		elem := ft.Elem()
		for elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct {
			checkType(elem, path+"[]", visited, missing)
		}
	case reflect.Map:
		val := ft.Elem()
		for val.Kind() == reflect.Ptr {
			val = val.Elem()
		}
		if val.Kind() == reflect.Struct {
			checkType(val, path+"[value]", visited, missing)
		}
	default:
		// Scalar types (bool, int, string, float, etc.) need no recursion.
	}
}
