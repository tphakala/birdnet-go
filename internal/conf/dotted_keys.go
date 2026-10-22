package conf

import (
	"reflect"
	"strings"

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
//     wrote for the module is silently dropped and the logger's
//     applyConfigDefaults then reinstates the module's defaultModuleOutputs
//     entry (console_also: true).
//
//   - logging.module_levels entries for dotted module names make
//     viper.Unmarshal fail outright ("expected type 'string', got
//     unconvertible type 'map[string]interface {}'"), which is fatal to
//     Load() and therefore to application startup.
//
// The application itself writes these dotted keys: the logger's
// defaultModuleOutputs registers "audio.ffmpeg", "spectrogram.prerenderer"
// and "analysis.processor" as first-class module names and the save path
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
//     Unmarshal (and therefore Load) from failing. The hook cannot recover
//     a parent and a dotted child that coexist ("audio" and "audio.ffmpeg"),
//     because AllSettings keeps only one of them.
//
//  2. For logging.modules the hook cannot tell a dotted-key remainder apart
//     from a legitimate map value by shape alone, so after Unmarshal the two
//     logging maps are re-decoded from viper's raw subtree, where the
//     original flat keys are still intact (viper.Get splits only the search
//     path, not the values it returns). This covers module_levels too, for
//     the parent plus dotted child case the hook cannot recover.
//
// The same split hits the two config maps whose keys the user types and whose
// values are structs: realtime.species.config ("St. Helena Plover") and the
// seasonal tracking seasons ("early.spring"). Unmarshal decodes "st" with a
// zero-value SpeciesConfig (or "early" with a zero Season, which then fails
// validation) and the real entry is lost. reDecodeDottedStructMaps re-decodes
// only the dotted keys from the raw subtree and drops the phantom prefix entry
// when no viper layer (file, defaults, environment, flags, overrides) really
// sets that prefix, which viper.IsSet answers for the exact path.
//
// reDecodeModuleOutputs applies the same rule to the zero-value entry Unmarshal
// leaves under the first segment of a dotted logging.modules name, so it is not
// written back to config.yaml by a later save.

const (
	// viperKeyDelimiter is the key delimiter viper splits config paths on.
	viperKeyDelimiter = "."

	// loggingModulesViperPath is the viper path of Settings.Logging.ModuleOutputs.
	loggingModulesViperPath = "logging.modules"

	// speciesConfigViperPath is the viper path of Settings.Realtime.Species.Config.
	speciesConfigViperPath = "realtime.species.config"

	// seasonalTrackingSeasonsViperPath is the viper path of
	// Settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons.
	seasonalTrackingSeasonsViperPath = "realtime.speciestracking.seasonaltracking.seasons"

	// opDecodeDottedMapKeys is the error context operation for the struct-map repair.
	opDecodeDottedMapKeys = "decode-dotted-map-keys"
)

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
// handled by reDecodeLoggingConfig and reDecodeDottedStructMaps instead, where
// the target type decides.
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
			flat[key+viperKeyDelimiter+nestedKey] = nestedValue
		}
	}
	return flat
}

// configDecodeHook is the one hook composition viper.Unmarshal in Load and every
// re-decode from the raw config subtree use, so their decode semantics cannot
// drift apart. Both hooks must be composed; a single one would drop the other.
func configDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(DottedKeyDecodeHook(), DurationDecodeHook())
}

// decodeRawEntry decodes one raw config value over *result with the same
// settings viper.Unmarshal uses, so entries re-read from the raw subtree behave
// exactly like entries Unmarshal decoded itself.
func decodeRawEntry(result, raw any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           result,
		WeaklyTypedInput: true,
		DecodeHook:       configDecodeHook(),
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
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
		// alone. Struct-valued maps (logging.modules, species config,
		// seasons) are repaired after Unmarshal from the raw config subtree instead.
		switch to.Elem().Kind() {
		case reflect.Interface, reflect.Map, reflect.Struct, reflect.Slice:
			return data, nil
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16,
			reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
			reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
			reflect.Float32, reflect.Float64, reflect.String:
			// scalar-valued maps: a nested map value is unambiguous
			// evidence of a key viper split on ".".
		default:
			// remaining kinds (Invalid, Chan, Func, Complex, Array,
			// UnsafePointer, Pointer) never appear as config targets.
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
// Unmarshal-time defaults. When Unmarshal produced no entry for the name
// (which is exactly what happens to dotted names, since viper split them and
// mapstructure never saw a key for the module), the base is the logger's
// built-in default for that module instead of a zero value, so a partial
// entry like "analysis.processor: {console_also: false}" keeps Enabled=true
// and its default file path rather than silently disabling the module's log
// file. Entries whose raw value is not a mapping are not decoded. For every
// dotted name the zero-value phantom entry Unmarshal left under its first
// segment is deleted unless viper.IsSet reports that segment as set.
func reDecodeModuleOutputs(settings *Settings, section map[string]any) error {
	rawModules, ok := section["modules"].(map[string]any)
	if !ok {
		return nil
	}
	if settings.Logging.ModuleOutputs == nil {
		settings.Logging.ModuleOutputs = make(map[string]logger.ModuleOutput, len(rawModules))
	}
	for name, rawValue := range rawModules {
		// Unmarshal left a zero-value phantom under the first segment of a
		// dotted name, even when the value is not a mapping, so the prefix is
		// checked before the value is skipped. It is dropped unless viper
		// reports the exact prefix path as set (a sibling entry in the file or
		// a built-in default such as audio), same rule as reDecodeDottedMapKeys.
		dropSplitPrefixEntry(settings.Logging.ModuleOutputs, loggingModulesViperPath, name)
		// A module entry must be a mapping. A scalar or null under a dotted
		// name was split by viper and ignored by Unmarshal, and a non-dotted
		// scalar already fails Unmarshal, so skipping leaves the entry out and
		// the logger's defaultModuleOutputs fills an absent known module.
		if _, isMap := rawValue.(map[string]any); !isMap {
			continue
		}
		entry, hasBase := settings.Logging.ModuleOutputs[name]
		if !hasBase {
			entry, _ = logger.DefaultModuleOutput(name)
		}
		if err := decodeRawEntry(&entry, rawValue); err != nil {
			// The decoder reports field paths relative to this entry only, so
			// name the module in the message as well as in the context.
			return errors.Newf("%s[%s]: %w", loggingModulesViperPath, name, err).
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
// The hook restores a lone dotted key, but when a parent and a dotted child
// are both set ("audio" and "audio.ffmpeg"), AllSettings has already dropped
// one of them (one path needs a string where the other needs a map), so this
// pass is required; the raw subtree keeps both.
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

// dropSplitPrefixEntry deletes the zero-value entry viper.Unmarshal left under
// the first segment of a dotted key, unless viper.IsSet reports that exact
// path as set (a sibling entry in the file or a default such as "audio"). A key
// without a dot is left alone.
func dropSplitPrefixEntry[V any](entries map[string]V, viperPath, key string) {
	prefix, _, dotted := strings.Cut(key, viperKeyDelimiter)
	if dotted && !viper.IsSet(viperPath+viperKeyDelimiter+prefix) {
		delete(entries, prefix)
	}
}

// reDecodeDottedStructMaps restores species config and season keys that contain
// a dot (see the file comment). Maps without dotted keys are left untouched.
func reDecodeDottedStructMaps(settings *Settings) error {
	species, err := reDecodeDottedMapKeys(settings.Realtime.Species.Config, speciesConfigViperPath)
	if err != nil {
		return err
	}
	settings.Realtime.Species.Config = species

	tracking := &settings.Realtime.SpeciesTracking.SeasonalTracking
	seasons, err := reDecodeDottedMapKeys(tracking.Seasons, seasonalTrackingSeasonsViperPath)
	if err != nil {
		return err
	}
	tracking.Seasons = seasons
	return nil
}

// reDecodeDottedMapKeys repairs a struct-valued map at viperPath whose
// user-typed keys contain ".". Each dotted key is decoded from the raw subtree
// (where viper.Get still returns the flat key) over a zero V and stored under
// its full key. Unmarshal meanwhile stored a zero-value phantom entry under the
// first segment of every such key; that entry is deleted unless viper.IsSet
// reports the segment as set. IsSet searches every layer (override, flags,
// environment, config file, defaults) but matches only the exact path, so a
// real "st" next to "st. helena plover" or a default "spring" next to
// "spring.late" survives with the value Unmarshal gave it.
//
// A dotted entry whose value is not a mapping is not stored (Unmarshal already
// ignored it as a split unknown field), but its phantom prefix entry is still
// dropped by the same rule. Maps without dotted keys are returned
// unchanged.
func reDecodeDottedMapKeys[V any](entries map[string]V, viperPath string) (map[string]V, error) {
	raw, ok := viper.Get(viperPath).(map[string]any)
	if !ok {
		return entries, nil
	}
	for key, rawValue := range raw {
		if !strings.Contains(key, viperKeyDelimiter) {
			continue
		}
		// Unmarshal left a phantom under the prefix even when the value is not
		// a mapping, so the prefix is checked before the value is skipped.
		dropSplitPrefixEntry(entries, viperPath, key)
		if _, isMap := rawValue.(map[string]any); !isMap {
			continue
		}
		var entry V
		if err := decodeRawEntry(&entry, rawValue); err != nil {
			// The decoder reports field paths relative to this entry only, so
			// name the path and key in the message as well as in the context.
			return nil, errors.Newf("%s[%s]: %w", viperPath, key, err).
				Category(errors.CategoryConfiguration).
				Context("operation", opDecodeDottedMapKeys).
				Context("key", key).
				Build()
		}
		if entries == nil {
			entries = make(map[string]V)
		}
		entries[key] = entry
	}
	return entries, nil
}
