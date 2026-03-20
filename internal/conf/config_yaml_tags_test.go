package conf

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

// TestSettingsYAMLRoundTrip verifies that a Settings struct survives
// yaml.Marshal → yaml.Unmarshal without data loss, sampling fields from every
// major subsystem. This catches missing yaml: tags that cause silent field drops.
func TestSettingsYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	original := Settings{}
	// EBird (the corrupted struct from #2429)
	original.Realtime.EBird.Locale = "en-uk"
	original.Realtime.EBird.CacheTTL = 48
	original.Realtime.EBird.APIKey = "test-key"
	// Retention (5 previously-mismatched fields)
	original.Realtime.Audio.Export.Retention.MaxAge = "30d"
	original.Realtime.Audio.Export.Retention.MaxUsage = "80%"
	original.Realtime.Audio.Export.Retention.MinClips = 10
	original.Realtime.Audio.Export.Retention.CheckInterval = 15
	// Dashboard
	original.Realtime.Dashboard.Thumbnails.ImageProvider = "avicommons"
	original.Realtime.Dashboard.SummaryLimit = 30
	original.Realtime.Dashboard.TemperatureUnit = "fahrenheit"
	// DynamicThreshold
	original.Realtime.DynamicThreshold.ValidHours = 24
	// RetrySettings (via Birdweather)
	original.Realtime.Birdweather.RetrySettings.MaxRetries = 5
	original.Realtime.Birdweather.RetrySettings.InitialDelay = 30
	// BirdNET
	original.BirdNET.Locale = "fi"
	original.BirdNET.UseXNNPACK = true
	// Security
	original.Security.SessionDuration = 168 * time.Hour
	// WebServer
	original.WebServer.Port = "8080"

	yamlData, err := yaml.Marshal(&original)
	require.NoError(t, err)

	var restored Settings
	err = yaml.Unmarshal(yamlData, &restored)
	require.NoError(t, err)

	// Verify fields from each major subsystem survived
	assert.Equal(t, "en-uk", restored.Realtime.EBird.Locale)
	assert.Equal(t, 48, restored.Realtime.EBird.CacheTTL)
	assert.Equal(t, "test-key", restored.Realtime.EBird.APIKey)
	assert.Equal(t, "30d", restored.Realtime.Audio.Export.Retention.MaxAge)
	assert.Equal(t, "80%", restored.Realtime.Audio.Export.Retention.MaxUsage)
	assert.Equal(t, 10, restored.Realtime.Audio.Export.Retention.MinClips)
	assert.Equal(t, 15, restored.Realtime.Audio.Export.Retention.CheckInterval)
	assert.Equal(t, "avicommons", restored.Realtime.Dashboard.Thumbnails.ImageProvider)
	assert.Equal(t, 30, restored.Realtime.Dashboard.SummaryLimit)
	assert.Equal(t, "fahrenheit", restored.Realtime.Dashboard.TemperatureUnit)
	assert.Equal(t, 24, restored.Realtime.DynamicThreshold.ValidHours)
	assert.Equal(t, 5, restored.Realtime.Birdweather.RetrySettings.MaxRetries)
	assert.Equal(t, 30, restored.Realtime.Birdweather.RetrySettings.InitialDelay)
	assert.Equal(t, "fi", restored.BirdNET.Locale)
	assert.True(t, restored.BirdNET.UseXNNPACK)
	assert.Equal(t, 168*time.Hour, restored.Security.SessionDuration)
	assert.Equal(t, "8080", restored.WebServer.Port)
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
