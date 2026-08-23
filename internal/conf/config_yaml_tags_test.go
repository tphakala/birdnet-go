package conf

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
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
	for t.Kind() == reflect.Pointer {
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

// TestAllSettingsYAMLKeysReachableByViper verifies that every field in the
// Settings tree carrying a yaml: key can actually be decoded by viper on load.
// conf.Load calls viper.Unmarshal with no TagName, so mapstructure matches on the
// mapstructure: tag when present, otherwise on the Go field name compared
// case-insensitively against the config key. The yaml: tag does NOT participate in
// loading. A field whose yaml key does not case-fold to its Go field name (e.g. a
// snake_case key like encryption_key) and carries no mapstructure: tag is silently
// dropped on load, even though SaveSettings writes it correctly through the yaml
// tag, so the value appears to persist and then quietly resets on the next load.
//
// This is the load-side sibling of TestAllSettingsStructsHaveYAMLTags, which only
// guards the save side. It is checked for every field in the hierarchy, not just
// leaves: an intermediate struct/slice/map field that is unreachable drops its
// whole block.
func TestAllSettingsYAMLKeysReachableByViper(t *testing.T) {
	t.Parallel()

	var unreachable []string
	visited := make(map[reflect.Type]bool)
	checkViperReachable(reflect.TypeFor[Settings](), "Settings", visited, &unreachable)

	for _, u := range unreachable {
		t.Logf("yaml key unreachable by viper: %s", u)
	}

	require.Empty(t, unreachable,
		"found %d field(s) whose yaml key viper cannot decode on load;\n"+
			"add a mapstructure: tag matching the yaml key (see HomeAssistantSettings.DiscoveryPrefix)",
		len(unreachable))
}

// TestViperDecodesBackupMapstructureKeys is the behavioral anchor for
// TestAllSettingsYAMLKeysReachableByViper. That test models mapstructure's
// key-matching rule by reflection; this one drives the real viper.Unmarshal path
// (the same DecodeHook Load uses) over a snake_case config and asserts a field
// whose yaml key does not case-fold to its Go field name actually loads. It pins
// the hand-rolled model against actual loader semantics, so the model cannot
// silently drift. discovery_prefix (realtime.mqtt.homeassistant) is a good anchor
// because it carries both a snake_case yaml key and an explicit mapstructure tag;
// without that tag viper would drop the key and the assertion would fail.
func TestViperDecodesMapstructureKeys(t *testing.T) {
	t.Parallel()

	const yamlCfg = `
realtime:
  mqtt:
    homeassistant:
      discovery_prefix: "test-prefix"
`
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yamlCfg)))

	var settings Settings
	require.NoError(t, v.Unmarshal(&settings, viper.DecodeHook(DurationDecodeHook())))

	assert.Equal(t, "test-prefix", settings.Realtime.MQTT.HomeAssistant.DiscoveryPrefix,
		"realtime.mqtt.homeassistant.discovery_prefix must decode through its mapstructure tag")
}

// checkViperReachable walks a struct type and records any field whose yaml key
// cannot be matched by mapstructure during viper.Unmarshal. See
// TestAllSettingsYAMLKeysReachableByViper for the invariant.
func checkViperReachable(t reflect.Type, path string, visited map[reflect.Type]bool, unreachable *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || visited[t] {
		return
	}
	visited[t] = true

	// Only recurse into project-owned or anonymous (inline) structs; stdlib and
	// third-party types (e.g. time.Time) are decoded by their own hooks.
	if t.PkgPath() != "" && !strings.HasPrefix(t.PkgPath(), projectModulePath) {
		return
	}

	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}

		fieldPath := fmt.Sprintf("%s.%s", path, f.Name)

		yamlKey, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		mapstructureTag, hasMapstructure := f.Tag.Lookup("mapstructure")
		mapstructureKey, _, _ := strings.Cut(mapstructureTag, ",")

		// A field with no yaml tag is loaded and saved under the same lowercased
		// field name, so it is inherently reachable. A yaml:"-" field is runtime
		// only and never loaded. Neither can be dropped by the mismatch this guards.
		if yamlKey != "" && yamlKey != "-" {
			// Work out the key viper decodes this field under and compare it to the
			// yaml save key, since that is the exact contract: what SaveSettings
			// writes must be what Load reads back. mapstructure matches its key
			// (the part before any options) case-insensitively; an empty key,
			// including mapstructure:",squash", falls back to the Go field name,
			// which is also viper's behavior with no tag at all. mapstructure:"-"
			// excludes the field from decoding entirely.
			switch {
			case hasMapstructure && mapstructureKey == "-":
				*unreachable = append(*unreachable,
					fmt.Sprintf("%s (yaml:%q is saved but mapstructure:%q skips it on load)",
						fieldPath, yamlKey, "-"))
			default:
				decodeKey := mapstructureKey
				if decodeKey == "" {
					decodeKey = f.Name
				}
				if !strings.EqualFold(decodeKey, yamlKey) {
					*unreachable = append(*unreachable,
						fmt.Sprintf("%s (yaml:%q is saved but viper decodes it under %q; add or fix the mapstructure tag)",
							fieldPath, yamlKey, decodeKey))
				}
			}
		}

		reachRecurse(f.Type, fieldPath, visited, unreachable)
	}
}

// reachRecurse resolves the element type for pointers, slices, and maps, then
// walks nested structs for viper reachability.
func reachRecurse(ft reflect.Type, path string, visited map[reflect.Type]bool, unreachable *[]string) {
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}

	switch ft.Kind() {
	case reflect.Struct:
		checkViperReachable(ft, path, visited, unreachable)
	case reflect.Slice, reflect.Array:
		// Recurse into the element type rather than checking only for a struct, so
		// nested containers ([][]T, []map[string]T) are unwrapped too. reachRecurse
		// strips pointers at its head, so []*T is still handled.
		reachRecurse(ft.Elem(), path+"[]", visited, unreachable)
	case reflect.Map:
		reachRecurse(ft.Elem(), path+"[value]", visited, unreachable)
	default:
		// Scalar types need no recursion.
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
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}

	switch ft.Kind() {
	case reflect.Struct:
		checkType(ft, path, visited, missing)
	case reflect.Slice:
		elem := ft.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct {
			checkType(elem, path+"[]", visited, missing)
		}
	case reflect.Map:
		val := ft.Elem()
		for val.Kind() == reflect.Pointer {
			val = val.Elem()
		}
		if val.Kind() == reflect.Struct {
			checkType(val, path+"[value]", visited, missing)
		}
	default:
		// Scalar types (bool, int, string, float, etc.) need no recursion.
	}
}
