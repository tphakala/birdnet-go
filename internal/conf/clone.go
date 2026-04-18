// clone.go: deep-copy helpers for *Settings. The writer path in the UI PUT
// handler uses CloneSettings before mutation so that concurrent readers
// observing the prior snapshot through atomic.Pointer.Load() never see torn
// or partially-updated values.

package conf

import (
	"maps"
	"slices"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// CloneSettings returns a deep copy of src that shares no slice or map backing
// arrays with src. The returned pointer is safe to mutate before atomically
// publishing via StoreSettings; readers holding the previous snapshot keep
// seeing consistent values because src is never touched.
//
// Every slice and map field reachable from the Settings struct must be cloned
// here. TestCloneSettings_DeepIndependence is the primary guardrail: any
// slice/map field added to Settings or a nested struct in the future MUST
// also get a clone line here and a matching assertion in the test, otherwise
// the settings race reintroduces silently through the shared backing store.
func CloneSettings(src *Settings) *Settings {
	if src == nil {
		return nil
	}

	// Shallow copy handles every value-type field (strings, ints, bools,
	// nested structs whose fields are all value types).
	dst := *src

	// Root-level slices and maps.
	dst.ValidationWarnings = slices.Clone(src.ValidationWarnings)
	dst.TaxonomySynonyms = maps.Clone(src.TaxonomySynonyms)

	// Logging.
	dst.Logging.ModuleOutputs = cloneModuleOutputs(src.Logging.ModuleOutputs)
	dst.Logging.ModuleLevels = maps.Clone(src.Logging.ModuleLevels)
	if src.Logging.Console != nil {
		c := *src.Logging.Console
		dst.Logging.Console = &c
	}
	if src.Logging.FileOutput != nil {
		f := *src.Logging.FileOutput
		dst.Logging.FileOutput = &f
	}

	// BirdNET.
	dst.BirdNET.Labels = slices.Clone(src.BirdNET.Labels)
	dst.BirdNET.RangeFilter.Species = slices.Clone(src.BirdNET.RangeFilter.Species)

	// Models.
	dst.Models.Enabled = slices.Clone(src.Models.Enabled)

	// Realtime.Audio.
	dst.Realtime.Audio.Sources = cloneAudioSources(src.Realtime.Audio.Sources)
	dst.Realtime.Audio.SoxAudioTypes = slices.Clone(src.Realtime.Audio.SoxAudioTypes)
	dst.Realtime.Audio.Equalizer.Filters = slices.Clone(src.Realtime.Audio.Equalizer.Filters)

	// Realtime.Dashboard.
	if src.Realtime.Dashboard.CustomColors != nil {
		c := *src.Realtime.Dashboard.CustomColors
		dst.Realtime.Dashboard.CustomColors = &c
	}
	dst.Realtime.Dashboard.Layout.Elements = cloneDashboardElements(src.Realtime.Dashboard.Layout.Elements)

	// Realtime.DogBarkFilter.
	dst.Realtime.DogBarkFilter.Species = slices.Clone(src.Realtime.DogBarkFilter.Species)

	// Realtime.DaylightFilter.
	dst.Realtime.DaylightFilter.Species = slices.Clone(src.Realtime.DaylightFilter.Species)

	// Realtime.RTSP.
	dst.Realtime.RTSP.Streams = cloneStreamConfigs(src.Realtime.RTSP.Streams)
	dst.Realtime.RTSP.URLs = slices.Clone(src.Realtime.RTSP.URLs)
	dst.Realtime.RTSP.FFmpegParameters = slices.Clone(src.Realtime.RTSP.FFmpegParameters)

	// Realtime.Monitoring.Disk.
	dst.Realtime.Monitoring.Disk.Paths = slices.Clone(src.Realtime.Monitoring.Disk.Paths)

	// Realtime.ExtendedCapture.
	dst.Realtime.ExtendedCapture.Species = slices.Clone(src.Realtime.ExtendedCapture.Species)

	// Realtime.Species.
	dst.Realtime.Species.Include = slices.Clone(src.Realtime.Species.Include)
	dst.Realtime.Species.Exclude = slices.Clone(src.Realtime.Species.Exclude)
	dst.Realtime.Species.Config = cloneSpeciesConfigMap(src.Realtime.Species.Config)

	// Realtime.SpeciesTracking.SeasonalTracking.Seasons: values are plain value
	// structs (no nested slices/maps), so maps.Clone is sufficient.
	dst.Realtime.SpeciesTracking.SeasonalTracking.Seasons = maps.Clone(src.Realtime.SpeciesTracking.SeasonalTracking.Seasons)

	// Security.
	dst.Security.OAuthProviders = cloneOAuthProviders(src.Security.OAuthProviders)

	// Backup.
	dst.Backup.Targets = cloneBackupTargets(src.Backup.Targets)
	dst.Backup.Schedules = slices.Clone(src.Backup.Schedules)

	// Notification.
	dst.Notification.Push.Providers = clonePushProviders(src.Notification.Push.Providers)

	return &dst
}

// cloneAudioSources deep-copies a slice of AudioSourceConfig so that the
// returned slice, its Models string slices, and any per-source Equalizer
// (with its Filters slice) share no backing storage with the input.
func cloneAudioSources(in []AudioSourceConfig) []AudioSourceConfig {
	if in == nil {
		return nil
	}
	out := make([]AudioSourceConfig, len(in))
	for i := range in {
		s := in[i]
		s.Models = slices.Clone(s.Models)
		if s.Equalizer != nil {
			eq := *s.Equalizer
			eq.Filters = slices.Clone(eq.Filters)
			s.Equalizer = &eq
		}
		out[i] = s
	}
	return out
}

// cloneStreamConfigs deep-copies a slice of StreamConfig, ensuring each
// StreamConfig's Models slice is independent.
func cloneStreamConfigs(in []StreamConfig) []StreamConfig {
	if in == nil {
		return nil
	}
	out := make([]StreamConfig, len(in))
	for i := range in {
		s := in[i]
		s.Models = slices.Clone(s.Models)
		out[i] = s
	}
	return out
}

// cloneDashboardElements deep-copies the dashboard layout elements slice
// and each element's optional config pointers. Inner *bool fields inside
// BannerConfig stay shared because the writer path replaces the parent
// pointer rather than mutating through it.
func cloneDashboardElements(in []DashboardElement) []DashboardElement {
	if in == nil {
		return nil
	}
	out := make([]DashboardElement, len(in))
	for i := range in {
		e := in[i]
		if e.Banner != nil {
			b := *e.Banner
			// BannerConfig carries two *bool fields (ShowPin,
			// MapExpandable). Independently clone each so a future
			// writer that toggles the boolean through the cloned
			// pointer does not mutate the snapshot readers hold.
			if b.ShowPin != nil {
				showPin := *b.ShowPin
				b.ShowPin = &showPin
			}
			if b.MapExpandable != nil {
				mapExpandable := *b.MapExpandable
				b.MapExpandable = &mapExpandable
			}
			e.Banner = &b
		}
		if e.Video != nil {
			v := *e.Video
			e.Video = &v
		}
		if e.Summary != nil {
			s := *e.Summary
			e.Summary = &s
		}
		if e.Grid != nil {
			g := *e.Grid
			e.Grid = &g
		}
		out[i] = e
	}
	return out
}

// cloneSpeciesConfigMap clones the per-species config map, deep-copying each
// SpeciesConfig so that nested Actions slices (and their Parameters slices)
// are independent.
func cloneSpeciesConfigMap(m map[string]SpeciesConfig) map[string]SpeciesConfig {
	if m == nil {
		return nil
	}
	out := make(map[string]SpeciesConfig, len(m))
	for k, v := range m {
		v.Actions = cloneSpeciesActions(v.Actions)
		out[k] = v
	}
	return out
}

// cloneSpeciesActions deep-copies a slice of SpeciesAction so that each
// action's Parameters slice is independent.
func cloneSpeciesActions(in []SpeciesAction) []SpeciesAction {
	if in == nil {
		return nil
	}
	out := make([]SpeciesAction, len(in))
	for i, a := range in {
		a.Parameters = slices.Clone(a.Parameters)
		out[i] = a
	}
	return out
}

// cloneOAuthProviders deep-copies the OAuth provider slice, ensuring each
// provider's Scopes slice is independent.
func cloneOAuthProviders(in []OAuthProviderConfig) []OAuthProviderConfig {
	if in == nil {
		return nil
	}
	out := make([]OAuthProviderConfig, len(in))
	for i := range in {
		p := in[i]
		p.Scopes = slices.Clone(p.Scopes)
		out[i] = p
	}
	return out
}

// cloneBackupTargets deep-copies the backup target slice, ensuring each
// target's Settings map is independent. The Settings map is type-deep-cloned
// because backup-type-specific config (rsync options, S3 prefixes, etc.) can
// nest slices or maps inside the any values; a plain maps.Clone would leave
// those backing arrays shared between src and dst.
func cloneBackupTargets(in []BackupTarget) []BackupTarget {
	if in == nil {
		return nil
	}
	out := make([]BackupTarget, len(in))
	for i, t := range in {
		t.Settings = cloneStringAnyMap(t.Settings)
		out[i] = t
	}
	return out
}

// cloneStringAnyMap returns a deep copy of a map[string]any whose values may
// themselves be maps or slices. Plain maps.Clone only copies the outer map;
// a writer appending to a shared nested slice would race against readers
// holding the previous snapshot. The copy is type-aware so numeric types
// (viper unmarshals int/float64 from YAML) are preserved exactly; a JSON
// round-trip would coerce int to float64 and break downstream consumers.
func cloneStringAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneAnyValue(v)
	}
	return out
}

// cloneAnyValue walks a value that came out of map[string]any and returns a
// deep copy for the composite cases (nested maps and slices). Scalar types
// (string, numbers, bool, time.Time, etc.) are value-copied by the caller
// when they are assigned through any, so they don't need special handling.
func cloneAnyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return cloneStringAnyMap(val)
	case map[string]string:
		return maps.Clone(val)
	case []any:
		out := make([]any, len(val))
		for i := range val {
			out[i] = cloneAnyValue(val[i])
		}
		return out
	case []string:
		return slices.Clone(val)
	case []byte:
		return slices.Clone(val)
	case []int:
		return slices.Clone(val)
	case []int64:
		return slices.Clone(val)
	case []float64:
		return slices.Clone(val)
	case []bool:
		return slices.Clone(val)
	default:
		// Scalar, typed struct, or pointer. For scalars (int, string, bool,
		// float64, time.Time) the any box copies the value. For typed
		// structs/pointers reaching this branch we trust the upstream
		// producer: the mutation pattern in api/v2 settings handlers
		// replaces whole any values rather than mutating through them.
		return val
	}
}

// cloneModuleOutputs deep-copies the module outputs map, including each
// module's optional *bool Compress pointer so writers that reassign the
// pointer on the clone don't share storage with readers.
func cloneModuleOutputs(m map[string]logger.ModuleOutput) map[string]logger.ModuleOutput {
	if m == nil {
		return nil
	}
	out := make(map[string]logger.ModuleOutput, len(m))
	for k, v := range m {
		if v.Compress != nil {
			c := *v.Compress
			v.Compress = &c
		}
		out[k] = v
	}
	return out
}

// clonePushProviders deep-copies push providers, including each provider's
// URLs, Args, Environment, Endpoints, and Filter sub-fields.
func clonePushProviders(in []PushProviderConfig) []PushProviderConfig {
	if in == nil {
		return nil
	}
	out := make([]PushProviderConfig, len(in))
	for i := range in {
		p := in[i]
		p.URLs = slices.Clone(p.URLs)
		p.Args = slices.Clone(p.Args)
		p.Environment = maps.Clone(p.Environment)
		p.Endpoints = cloneWebhookEndpoints(p.Endpoints)
		p.Filter.Types = slices.Clone(p.Filter.Types)
		p.Filter.Priorities = slices.Clone(p.Filter.Priorities)
		p.Filter.Components = slices.Clone(p.Filter.Components)
		p.Filter.MetadataFilters = cloneStringAnyMap(p.Filter.MetadataFilters)
		out[i] = p
	}
	return out
}

// cloneWebhookEndpoints deep-copies webhook endpoints, ensuring each
// endpoint's Headers map is independent.
func cloneWebhookEndpoints(in []WebhookEndpointConfig) []WebhookEndpointConfig {
	if in == nil {
		return nil
	}
	out := make([]WebhookEndpointConfig, len(in))
	for i := range in {
		e := in[i]
		e.Headers = maps.Clone(e.Headers)
		out[i] = e
	}
	return out
}
