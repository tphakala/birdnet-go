package conf

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration with JSON and YAML serialization that uses
// human-readable strings (e.g., "30s") instead of nanosecond integers.
// This prevents corruption when duration values round-trip through JSON
// (where time.Duration serializes as raw nanoseconds that frontends
// misinterpret as human-scale numbers).
type Duration time.Duration

// Std converts Duration to a standard time.Duration.
func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

// MarshalJSON outputs the duration as a JSON string like "30s".
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts a JSON string ("30s"), a number (nanoseconds for
// backward compatibility), or null (resets to zero).
func (d *Duration) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}

	switch value := v.(type) {
	case string:
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration string %q: %w", value, err)
		}
		*d = Duration(parsed)
	case float64:
		// Backward compat: treat as nanoseconds (Go's native representation)
		*d = Duration(time.Duration(int64(value)))
	case nil:
		// JSON null resets to zero duration (matches standard json.Unmarshal behavior)
		*d = 0
	default:
		return fmt.Errorf("invalid duration value: %v (type %T)", v, v)
	}
	return nil
}

// MarshalYAML outputs the duration as a human-readable string.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// UnmarshalYAML accepts a duration string ("30s") or a bare integer
// (nanoseconds, for backward compatibility with legacy/corrupted configs).
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// Try parsing as duration string first (e.g., "30s", "5m")
		if parsed, err := time.ParseDuration(value.Value); err == nil {
			*d = Duration(parsed)
			return nil
		}
		// Fallback: try parsing as bare integer (nanoseconds) for backward compat
		// with legacy configs that may contain values like "30000000000" or "300"
		if nanos, err := strconv.ParseInt(value.Value, 10, 64); err == nil {
			*d = Duration(time.Duration(nanos))
			return nil
		}
		return fmt.Errorf("invalid duration %q: expected format like \"30s\" or \"5m\"", value.Value)
	default:
		return fmt.Errorf("expected scalar duration value, got %v", value.Kind)
	}
}

// durationType is the reflect.Type for conf.Duration, cached for the decode hook.
var durationType = reflect.TypeFor[Duration]()

// DurationDecodeHook returns a mapstructure DecodeHookFunc that converts
// string values (e.g., "30s") to conf.Duration. This is needed because
// Viper's built-in StringToTimeDurationHookFunc only handles time.Duration,
// not our custom Duration type.
//
// It also composes with Viper's default hooks so that standard conversions
// (e.g., string → time.Duration for other fields) continue to work.
func DurationDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		// Handle conf.Duration fields
		mapstructure.DecodeHookFuncType(func(from, to reflect.Type, data any) (any, error) {
			if to != durationType {
				return data, nil
			}

			switch v := data.(type) {
			case string:
				parsed, err := time.ParseDuration(v)
				if err != nil {
					return nil, fmt.Errorf("invalid duration %q: %w", v, err)
				}
				return Duration(parsed), nil
			case int64:
				return Duration(time.Duration(v)), nil
			case float64:
				return Duration(time.Duration(int64(v))), nil
			default:
				return data, nil
			}
		}),
		// Preserve Viper's default hooks for time.Duration and other types
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	)
}
