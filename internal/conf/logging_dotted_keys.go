package conf

import (
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Viper flattens nested YAML maps into dot-delimited keys internally and
// splits on "." again while decoding into structs. A logging module whose
// name contains a dot (e.g. "analysis.processor") therefore never reaches the
// LoggingConfig maps through the main unmarshal pass: viper re-parses
// `modules: {analysis.processor: {...}}` as a nested analysis -> processor
// path. The visible symptoms are
//
//   - logging.modules: the dotted module's settings vanish and a phantom
//     zero-value entry appears under the first dot segment, and
//   - logging.module_levels: the dotted module's scalar value is parsed as a
//     nested map, which used to make the whole viper.Unmarshal call fail
//     ("expected type 'string', got unconvertible type 'map[string]interface
//     {}'"), so a hand-edited config stopped the app from booting (#4523).
//
// Viper's own searchPath machinery cannot represent these keys, but its raw
// config tree still holds them intact: v.Get("logging") returns the
// unflattened map with "analysis.processor" as a single key. The raw tree
// carries only what the config file says, so this pass must merge, not
// replace: the main unmarshal is the only pass that sees viper's defaults
// (22 default module entries), and dropping those would change SaveSettings
// output. restoreDottedLoggingKeys therefore decodes the raw modules map,
// writes entries into the existing map, and deletes the phantom first-segment
// entries the delimiter confusion fabricates.
//
// Only LoggingConfig needs this: its two maps are keyed by free-form module
// names that may contain dots. Every other Settings map uses fixed keys.
const (
	rawLoggingModulesKey      = "modules"
	rawLoggingModuleLevelsKey = "module_levels"
)

// restoreDottedLoggingKeys re-decodes LoggingConfig.ModuleOutputs and
// LoggingConfig.ModuleLevels from viper's raw, unflattened config tree so
// module names containing a dot survive the load.
//
// The raw tree carries only file config, so entries are merged into the maps
// the main pass produced (which include viper defaults) instead of replacing
// them. The phantom zero-value entry that the main pass fabricates for the
// first dot segment (e.g. a bogus "analysis" module when the file configures
// "analysis.processor") is removed so it cannot shadow the real module's
// defaults or leak into SaveSettings output.
func restoreDottedLoggingKeys(v *viper.Viper, settings *Settings) {
	rawLogging, ok := v.Get("logging").(map[string]any)
	if !ok {
		return // no logging section in the file: defaults already decoded by the main pass
	}

	restoreModuleOutputs(rawLogging, settings)
	restoreModuleLevels(rawLogging, settings)
}

// restoreModuleOutputs merges file-configured module output entries over the
// main pass result and removes the phantom first-segment entries.
func restoreModuleOutputs(rawLogging map[string]any, settings *Settings) {
	rawModules, ok := rawLogging[rawLoggingModulesKey].(map[string]any)
	if !ok || len(rawModules) == 0 {
		return
	}

	if settings.Logging.ModuleOutputs == nil {
		settings.Logging.ModuleOutputs = make(map[string]logger.ModuleOutput, len(rawModules))
	}

	for name, raw := range rawModules {
		if !strings.Contains(name, ".") {
			// Plain names were decoded correctly by the main pass; re-decoding
			// from the raw tree would lose nothing but also gain nothing.
			continue
		}
		var out logger.ModuleOutput
		dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			Result:           &out,
			WeaklyTypedInput: true,
			TagName:          "mapstructure",
		})
		if err != nil {
			continue // decoder setup failure: keep the main pass result
		}
		if err := dec.Decode(raw); err != nil {
			continue // malformed entry: keep the main pass result rather than fail the load
		}
		settings.Logging.ModuleOutputs[name] = out

		// Remove the phantom entry the delimiter confusion fabricated for the
		// first dot segment (e.g. bogus "analysis" when the file says
		// "analysis.processor"), unless the file also configures that plain
		// name in its own right.
		if first := name[:strings.Index(name, ".")]; first != "" {
			if _, configured := rawModules[first]; !configured {
				delete(settings.Logging.ModuleOutputs, first)
			}
		}
	}
}

// restoreModuleLevels merges file-configured per-module log levels over the
// main pass result.
func restoreModuleLevels(rawLogging map[string]any, settings *Settings) {
	rawLevels, ok := rawLogging[rawLoggingModuleLevelsKey].(map[string]any)
	if !ok || len(rawLevels) == 0 {
		return
	}

	if settings.Logging.ModuleLevels == nil {
		settings.Logging.ModuleLevels = make(map[string]string, len(rawLevels))
	}

	for name, raw := range rawLevels {
		if !strings.Contains(name, ".") {
			continue // decoded correctly by the main pass
		}
		str, ok := raw.(string)
		if !ok {
			// The tolerant hook already let the unmarshal pass; a non-string
			// value here is a genuinely malformed entry, keep the main pass result.
			continue
		}
		settings.Logging.ModuleLevels[name] = str

		// Remove the phantom first-segment key, if any.
		if first := name[:strings.Index(name, ".")]; first != "" {
			if _, configured := rawLevels[first]; !configured {
				delete(settings.Logging.ModuleLevels, first)
			}
		}
	}
}
