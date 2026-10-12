package conf

import (
	"reflect"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// This file repairs viper's handling of configuration keys that contain a
// dot ("."). Viper treats "." as the key delimiter at every layer of the
// config tree, so a flat map entry written by yaml.Marshal or by hand, e.g.
//
//	logging:
//	  modules:
//	    analysis.processor:
//	      console_also: false
//
// is split into nested maps (modules -> analysis -> processor -> ...) before
// it reaches mapstructure. Two visible failures follow from that, both filed
// as #4523:
//
//   - logging.modules entries for dotted module names decode as key
//     "analysis" with a zero-value ModuleOutput, so every setting the user
//     wrote for the module is silently dropped and applyConfigDefaults'
//     ensureModuleOutput reinstates the default (console_also: true).
//
//   - logging.module_levels entries for dotted module names make
//     viper.Unmarshal fail outright ("expected type 'string', got
//     unconvertible type 'map[string]interface {}'"), which is fatal to
//     Load() and therefore to application startup.
//
// The application itself writes these dotted keys: ensureModuleOutput
// registers "audio.ffmpeg", "spectrogram.prerenderer" and
// "analysis.processor" as first-class module names and the save path
// (yaml.Marshal) round-trips them verbatim. The write side is correct YAML;
// the read side is what loses them.
//
// The repair is two-fold because the loss happens in two different places:
//
//  1. AllSettings() flattens the whole tree into delimiter-joined paths and
//     re-splits them, so by the time mapstructure runs, module_levels'
//     "spectrogram.prerenderer" has become a nested map that cannot decode
//     into string at all. A decode hook flattens those nested values back
//     into dotted keys before mapstructure decodes them, which keeps
//     Unmarshal (and therefore Load) from failing.
//
//  2. For logging.modules the hook cannot tell a dotted-key remainder apart
//     from a legitimate map value by shape alone, so after Unmarshal the two
//     logging maps are re-decoded from viper's raw subtree, where the
//     original flat keys are still intact (viper.Get splits only the search
//     path, not the values it returns).

// decodeHookFlattenMaxDepth bounds the nesting a malformed config can drive the
// hook through before it gives up. Real dotted keys are one level deep (the
// split map); the bound exists so a hand-written adversarial file cannot
// recurse without limit.
const decodeHookFlattenMaxDepth = 32

// flattenDottedMapKeys rewrites a map that viper has split on "." so entries
// whose value is itself a map are merged back under their joined key.
//
//	data = map[string]any{
//	    "birdnet":     map[string]any{"level": "debug"},
//	    "spectrogram": map[string]any{"prerenderer": "warn"},
//	}
//	-> map[string]any{
//	    "birdnet":                 "..." (untouched),
//	    "spectrogram.prerenderer": "warn",
//	}
//
// Only maps whose decoded value type is a scalar (string, bool, numbers)
// need this: for those, a map value can never be legitimate input, so any
// nested map is unambiguously a split dotted key. Struct-valued maps are
// handled by reDecodeLoggingConfig instead, where the target type decides.
func flattenDottedMapKeys(data map[string]any, depth int) map[string]any {
	if depth > decodeHookFlattenMaxDepth || data == nil {
		return data
	}
	flat := make(map[string]any, len(data))
	for key, value := range data {
		nested, ok := value.(map[string]any)
		if !ok {
			flat[key] = value
			continue
		}
		for nestedKey, nestedValue := range flattenDottedMapKeys(nested, depth+1) {
			flat[key+"."+nestedKey] = nestedValue
		}
	}
	return flat
}

// DottedKeyDecodeHook returns a mapstructure hook that restores flat map
// keys viper split on ".". It only rewrites maps whose value type is scalar,
// where a nested map value is unambiguous evidence of a split key.
func DottedKeyDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.DecodeHookFuncType(func(from, to reflect.Type, data any) (any, error) {
		if to.Kind() != reflect.Map {
			return data, nil
		}
		// map[string]any targets legitimately hold nested maps (e.g.
		// realtime.metadata_filters, backup target settings); leave those
		// alone. Struct-valued maps (logging.modules) are repaired after
		// Unmarshal from the raw config subtree instead.
		switch to.Elem().Kind() {
		case reflect.Interface, reflect.Map, reflect.Struct, reflect.Slice:
			return data, nil
		}
		source, ok := data.(map[string]any)
		if !ok {
			return data, nil
		}
		return flattenDottedMapKeys(source, 0), nil
	})
}

// reDecodeLoggingConfig restores module keys that viper split on "." (see the
// file comment). It runs after viper.Unmarshal: AllSettings() has already
// re-split "analysis.processor" into nested maps there, but the raw subtree
// that viper.Get returns still holds the original flat keys, so the two
// logging maps are re-read from it.
//
// Unmarshal's output is the starting point, not a throwaway: each entry it
// produced (including the defaults viper seeded for standard modules) keeps
// its fields, and the raw value for that entry only overwrites the fields
// the config file actually sets. A module absent from the file is therefore
// untouched, and a module present in the file decodes with its defaults
// still applied - the same field-level precedence Unmarshal would have
// produced if viper had not split the key.
func reDecodeLoggingConfig(settings *Settings) error {
	raw := viper.Get("logging")
	if raw == nil {
		return nil
	}
	section, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	if err := reDecodeModuleOutputs(settings, section); err != nil {
		return err
	}
	return reDecodeModuleLevels(settings, section)
}

// reDecodeModuleOutputs rebuilds settings.Logging.ModuleOutputs from the raw
// logging section. For every flat module key the raw section carries - dotted
// or not - the entry produced by Unmarshal is used as the base and the raw
// value is decoded over it, so fields the file does not set keep their
// Unmarshal-time defaults.
func reDecodeModuleOutputs(settings *Settings, section map[string]any) error {
	rawModules, ok := section["modules"].(map[string]any)
	if !ok {
		return nil
	}
	if settings.Logging.ModuleOutputs == nil {
		settings.Logging.ModuleOutputs = make(map[string]logger.ModuleOutput, len(rawModules))
	}
	for name, rawValue := range rawModules {
		entry := settings.Logging.ModuleOutputs[name]
		decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			Result:           &entry,
			WeaklyTypedInput: true,
			DecodeHook:       mapstructure.ComposeDecodeHookFunc(DottedKeyDecodeHook(), DurationDecodeHook()),
		})
		if err != nil {
			return errors.New(err).
				Category(errors.CategoryConfiguration).
				Context("operation", "decode-logging-modules").
				Context("module", name).
				Build()
		}
		if err := decoder.Decode(rawValue); err != nil {
			return errors.New(err).
				Category(errors.CategoryConfiguration).
				Context("operation", "decode-logging-modules").
				Context("module", name).
				Build()
		}
		settings.Logging.ModuleOutputs[name] = entry
	}
	return nil
}

// reDecodeModuleLevels rebuilds settings.Logging.ModuleLevels the same way.
// Unmarshal already decodes module_levels with the DottedKeyDecodeHook
// composed in (scalar-valued maps are exactly what the hook flattens), so
// this pass is belt-and-braces: it costs one lookup and guarantees the two
// maps are always restored from the same raw subtree, whether Unmarshal's
// path or this one is what a future refactor changes first.
func reDecodeModuleLevels(settings *Settings, section map[string]any) error {
	rawLevels, ok := section["module_levels"].(map[string]any)
	if !ok {
		return nil
	}
	if settings.Logging.ModuleLevels == nil {
		settings.Logging.ModuleLevels = make(map[string]string, len(rawLevels))
	}
	for name, rawValue := range rawLevels {
		// Non-string values are not this repair's concern: Unmarshal (with
		// the composed hook) has already produced the same weak conversion
		// for them it always did, and overwriting that here would change
		// behavior beyond restoring dotted keys.
		level, ok := rawValue.(string)
		if !ok {
			continue
		}
		settings.Logging.ModuleLevels[name] = level
	}
	return nil
}
