package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// mutated is the sentinel string the clone-independence test writes through
// the clone. If it leaks back to src, assertSourceUnchanged catches it.
const mutated = "MUTATED"

// TestCloneSettings_Nil verifies that CloneSettings returns nil for a nil input
// so callers can safely clone an uninitialised global.
func TestCloneSettings_Nil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, CloneSettings(nil))
}

// TestCloneSettings_NewPointer verifies that CloneSettings returns a fresh
// *Settings, not an alias of src.
func TestCloneSettings_NewPointer(t *testing.T) {
	t.Parallel()
	src := &Settings{}
	dst := CloneSettings(src)
	require.NotNil(t, dst)
	require.NotSame(t, src, dst, "CloneSettings must return a new pointer")
}

// TestCloneSettings_DeepIndependence exercises every slice and map field
// reachable from *Settings. It populates src, clones, mutates the clone, and
// asserts the source is untouched. This test is the primary guardrail against
// field drift: if a new slice or map is added to Settings (or a nested struct)
// the test must grow a matching case, or CloneSettings will silently share a
// backing array and reintroduce the settings race.
func TestCloneSettings_DeepIndependence(t *testing.T) {
	t.Parallel()

	src := newPopulatedSettings()
	dst := CloneSettings(src)
	require.NotNil(t, dst)

	mutateCloneEverywhere(dst)

	// Assert every src slice/map still holds its original values.
	assertSourceUnchanged(t, src)
}

// newPopulatedSettings returns a *Settings with every slice and map field
// populated so the clone/mutate cycle has something to prove independence on.
// Extend this helper whenever a new slice or map field is added to Settings.
func newPopulatedSettings() *Settings {
	compress := true
	s := &Settings{}
	s.ValidationWarnings = []string{"warn-1", "warn-2"}
	s.TaxonomySynonyms = map[string]string{"American Robin": "Turdus migratorius"}

	s.Logging.ModuleLevels = map[string]string{"audio": "debug"}
	s.Logging.ModuleOutputs = map[string]logger.ModuleOutput{
		"audio": {Enabled: true, FilePath: "logs/audio.log", Compress: &compress},
	}
	s.Logging.Console = &logger.ConsoleOutput{Enabled: true, Level: "info"}
	s.Logging.FileOutput = &logger.FileOutput{Enabled: true, Path: "logs/app.log"}

	s.BirdNET.Labels = []string{"alpha", "beta"}
	s.BirdNET.RangeFilter.Species = []string{"Turdus merula", "Parus major"}

	s.Models.Enabled = []string{"birdnet", "perch_v2"}

	s.Realtime.Audio.Sources = []AudioSourceConfig{
		{
			Name:   "front",
			Models: []string{"birdnet"},
			Equalizer: &EqualizerSettings{
				Enabled: true,
				Filters: []EqualizerFilter{{Type: "LowPass", Frequency: 100}},
			},
		},
	}
	s.Realtime.Audio.SoxAudioTypes = []string{"wav", "flac"}
	s.Realtime.Audio.Equalizer.Filters = []EqualizerFilter{{Type: "HighPass", Frequency: 200}}

	s.Realtime.Dashboard.CustomColors = &CustomColors{Primary: "#2563eb"}
	s.Realtime.Dashboard.Layout.Elements = []DashboardElement{
		{
			ID:      "banner-0",
			Type:    "banner",
			Enabled: true,
			Banner:  &BannerConfig{Title: "Home"},
			Video:   &VideoEmbedConfig{URL: "https://youtu.be/abc"},
			Summary: &DailySummaryConfig{SummaryLimit: 30},
			Grid:    &DetectionsGridConfig{},
		},
	}

	s.Realtime.DogBarkFilter.Species = []string{"Canis familiaris"}
	s.Realtime.DaylightFilter.Species = []string{"Strix aluco"}

	s.Realtime.RTSP.Streams = []StreamConfig{
		{Name: "front", URL: "rtsp://x", Enabled: true, Models: []string{"birdnet"}},
	}
	s.Realtime.RTSP.URLs = []string{"rtsp://legacy"}
	s.Realtime.RTSP.FFmpegParameters = []string{"-rtsp_transport", "tcp"}

	s.Realtime.Monitoring.Disk.Paths = []string{"/", "/data"}

	s.Realtime.ExtendedCapture.Species = []string{"Tyto alba"}

	s.Realtime.Species.Include = []string{"Corvus corax"}
	s.Realtime.Species.Exclude = []string{"Passer domesticus"}
	s.Realtime.Species.Config = map[string]SpeciesConfig{
		"Turdus merula": {
			Threshold: 0.5,
			Actions: []SpeciesAction{
				{Type: "ExecuteCommand", Command: "/bin/echo", Parameters: []string{"hi"}},
			},
		},
	}

	s.Realtime.SpeciesTracking.SeasonalTracking.Seasons = map[string]Season{
		"winter": {StartMonth: 12, StartDay: 1},
	}

	s.Security.OAuthProviders = []OAuthProviderConfig{
		{Provider: "google", Scopes: []string{"openid", "email"}},
	}

	// Include nested slices and nested maps inside the Settings map[string]any
	// so the clone test catches a regression if the deep copy ever degrades
	// back to a plain maps.Clone on the outer map only. rsync-style options
	// are the realistic case: a []string value shared through map[string]any.
	s.Backup.Targets = []BackupTarget{
		{
			Type:    "rsync",
			Enabled: true,
			Settings: map[string]any{
				"path":    "/backups",
				"options": []any{"--archive", "--compress"},
				"ssh": map[string]any{
					"keyPath": "/home/user/.ssh/id_rsa",
					"ports":   []any{22, 2222},
				},
			},
		},
	}
	s.Backup.Schedules = []BackupScheduleConfig{{Enabled: true, Hour: 3}}

	s.Notification.Push.Providers = []PushProviderConfig{
		{
			Type:        "webhook",
			URLs:        []string{"https://hook.example/api"},
			Args:        []string{"--flag"},
			Environment: map[string]string{"API_KEY": "secret"},
			Endpoints: []WebhookEndpointConfig{
				{URL: "https://a", Headers: map[string]string{"X-Custom": "1"}},
			},
			Filter: PushFilterConfig{
				Types:      []string{"new_species"},
				Priorities: []string{"high"},
				Components: []string{"detection"},
				// Nested slice inside MetadataFilters exercises the deep-clone
				// path (same concern as BackupTarget.Settings above).
				MetadataFilters: map[string]any{
					"location": "garden",
					"tags":     []any{"urgent", "bird-of-prey"},
				},
			},
		},
	}

	return s
}

// mutateCloneEverywhere touches every slice, map, and owned pointer on dst so
// that any accidental backing-store sharing with src surfaces as a leaked
// mutation on src in assertSourceUnchanged.
func mutateCloneEverywhere(dst *Settings) {
	dst.ValidationWarnings[0] = mutated
	dst.ValidationWarnings = append(dst.ValidationWarnings, "added")
	dst.TaxonomySynonyms["American Robin"] = mutated
	dst.TaxonomySynonyms["new-key"] = "value"

	dst.Logging.ModuleLevels["audio"] = mutated
	dst.Logging.ModuleLevels["added"] = "debug"
	m := dst.Logging.ModuleOutputs["audio"]
	m.Enabled = false
	if m.Compress != nil {
		*m.Compress = false
	}
	dst.Logging.ModuleOutputs["audio"] = m
	dst.Logging.ModuleOutputs["added"] = logger.ModuleOutput{Enabled: true}
	dst.Logging.Console.Level = mutated
	dst.Logging.FileOutput.Path = mutated

	dst.BirdNET.Labels[0] = mutated
	dst.BirdNET.RangeFilter.Species = append(dst.BirdNET.RangeFilter.Species, "Corvus corax")

	dst.Models.Enabled[0] = mutated

	dst.Realtime.Audio.Sources[0].Name = mutated
	dst.Realtime.Audio.Sources[0].Models[0] = mutated
	dst.Realtime.Audio.Sources[0].Equalizer.Enabled = false
	dst.Realtime.Audio.Sources[0].Equalizer.Filters[0].Type = mutated
	dst.Realtime.Audio.SoxAudioTypes[0] = mutated
	dst.Realtime.Audio.Equalizer.Filters[0].Type = mutated

	dst.Realtime.Dashboard.CustomColors.Primary = mutated
	dst.Realtime.Dashboard.Layout.Elements[0].Banner.Title = mutated
	dst.Realtime.Dashboard.Layout.Elements[0].Video.URL = mutated
	dst.Realtime.Dashboard.Layout.Elements[0].Summary.SummaryLimit = 0
	dst.Realtime.Dashboard.Layout.Elements[0].ID = mutated
	dst.Realtime.Dashboard.Layout.Elements = append(dst.Realtime.Dashboard.Layout.Elements,
		DashboardElement{ID: "added"})

	dst.Realtime.DogBarkFilter.Species[0] = mutated
	dst.Realtime.DaylightFilter.Species[0] = mutated

	dst.Realtime.RTSP.Streams[0].Models[0] = mutated
	dst.Realtime.RTSP.Streams[0].Enabled = false
	dst.Realtime.RTSP.URLs[0] = mutated
	dst.Realtime.RTSP.FFmpegParameters[0] = mutated

	dst.Realtime.Monitoring.Disk.Paths[0] = mutated

	dst.Realtime.ExtendedCapture.Species[0] = mutated

	dst.Realtime.Species.Include[0] = mutated
	dst.Realtime.Species.Exclude[0] = mutated
	sc := dst.Realtime.Species.Config["Turdus merula"]
	sc.Threshold = 0.99
	sc.Actions[0].Parameters[0] = mutated
	dst.Realtime.Species.Config["Turdus merula"] = sc
	dst.Realtime.Species.Config[mutated] = SpeciesConfig{Threshold: 0.1}

	dst.Realtime.SpeciesTracking.SeasonalTracking.Seasons["winter"] = Season{StartMonth: 1}
	dst.Realtime.SpeciesTracking.SeasonalTracking.Seasons[mutated] = Season{}

	dst.Security.OAuthProviders[0].Scopes[0] = mutated

	dst.Backup.Targets[0].Settings["path"] = mutated
	// Mutate nested slice and nested map inside the any-typed Settings so
	// src keeps its original values only if cloneStringAnyMap walks the
	// tree. If a future refactor degrades this to a plain maps.Clone the
	// assertions in assertSourceUnchanged will flag the regression.
	if opts, ok := dst.Backup.Targets[0].Settings["options"].([]any); ok {
		opts[0] = mutated
	}
	if ssh, ok := dst.Backup.Targets[0].Settings["ssh"].(map[string]any); ok {
		ssh["keyPath"] = mutated
		if ports, ok := ssh["ports"].([]any); ok {
			ports[0] = 0
		}
	}
	dst.Backup.Schedules[0].Hour = 99

	pp := dst.Notification.Push.Providers[0]
	pp.URLs[0] = mutated
	pp.Args[0] = mutated
	pp.Environment["API_KEY"] = mutated
	pp.Endpoints[0].URL = mutated
	pp.Endpoints[0].Headers["X-Custom"] = mutated
	pp.Filter.Types[0] = mutated
	pp.Filter.Priorities[0] = mutated
	pp.Filter.Components[0] = mutated
	pp.Filter.MetadataFilters["location"] = mutated
	if tags, ok := pp.Filter.MetadataFilters["tags"].([]any); ok {
		tags[0] = mutated
	}
	dst.Notification.Push.Providers[0] = pp
}

// assertSourceUnchanged verifies that every field touched by
// mutateCloneEverywhere still holds its original value on src.
func assertSourceUnchanged(t *testing.T, src *Settings) {
	t.Helper()

	assert.Equal(t, []string{"warn-1", "warn-2"}, src.ValidationWarnings)
	assert.Equal(t, map[string]string{"American Robin": "Turdus migratorius"}, src.TaxonomySynonyms)

	assert.Equal(t, map[string]string{"audio": "debug"}, src.Logging.ModuleLevels)
	require.Contains(t, src.Logging.ModuleOutputs, "audio")
	assert.True(t, src.Logging.ModuleOutputs["audio"].Enabled)
	require.NotNil(t, src.Logging.ModuleOutputs["audio"].Compress)
	assert.True(t, *src.Logging.ModuleOutputs["audio"].Compress)
	_, hasAdded := src.Logging.ModuleOutputs["added"]
	assert.False(t, hasAdded, "module outputs on src must not contain the added key")
	require.NotNil(t, src.Logging.Console)
	assert.Equal(t, "info", src.Logging.Console.Level)
	require.NotNil(t, src.Logging.FileOutput)
	assert.Equal(t, "logs/app.log", src.Logging.FileOutput.Path)

	assert.Equal(t, []string{"alpha", "beta"}, src.BirdNET.Labels)
	assert.Equal(t, []string{"Turdus merula", "Parus major"}, src.BirdNET.RangeFilter.Species)

	assert.Equal(t, []string{"birdnet", "perch_v2"}, src.Models.Enabled)

	require.Len(t, src.Realtime.Audio.Sources, 1)
	assert.Equal(t, "front", src.Realtime.Audio.Sources[0].Name)
	assert.Equal(t, []string{"birdnet"}, src.Realtime.Audio.Sources[0].Models)
	require.NotNil(t, src.Realtime.Audio.Sources[0].Equalizer)
	assert.True(t, src.Realtime.Audio.Sources[0].Equalizer.Enabled)
	require.Len(t, src.Realtime.Audio.Sources[0].Equalizer.Filters, 1)
	assert.Equal(t, "LowPass", src.Realtime.Audio.Sources[0].Equalizer.Filters[0].Type)
	assert.Equal(t, []string{"wav", "flac"}, src.Realtime.Audio.SoxAudioTypes)
	require.Len(t, src.Realtime.Audio.Equalizer.Filters, 1)
	assert.Equal(t, "HighPass", src.Realtime.Audio.Equalizer.Filters[0].Type)

	require.NotNil(t, src.Realtime.Dashboard.CustomColors)
	assert.Equal(t, "#2563eb", src.Realtime.Dashboard.CustomColors.Primary)
	require.Len(t, src.Realtime.Dashboard.Layout.Elements, 1)
	elem := src.Realtime.Dashboard.Layout.Elements[0]
	assert.Equal(t, "banner-0", elem.ID)
	require.NotNil(t, elem.Banner)
	assert.Equal(t, "Home", elem.Banner.Title)
	require.NotNil(t, elem.Video)
	assert.Equal(t, "https://youtu.be/abc", elem.Video.URL)
	require.NotNil(t, elem.Summary)
	assert.Equal(t, 30, elem.Summary.SummaryLimit)

	assert.Equal(t, []string{"Canis familiaris"}, src.Realtime.DogBarkFilter.Species)
	assert.Equal(t, []string{"Strix aluco"}, src.Realtime.DaylightFilter.Species)

	require.Len(t, src.Realtime.RTSP.Streams, 1)
	assert.True(t, src.Realtime.RTSP.Streams[0].Enabled)
	assert.Equal(t, []string{"birdnet"}, src.Realtime.RTSP.Streams[0].Models)
	assert.Equal(t, []string{"rtsp://legacy"}, src.Realtime.RTSP.URLs)
	assert.Equal(t, []string{"-rtsp_transport", "tcp"}, src.Realtime.RTSP.FFmpegParameters)

	assert.Equal(t, []string{"/", "/data"}, src.Realtime.Monitoring.Disk.Paths)

	assert.Equal(t, []string{"Tyto alba"}, src.Realtime.ExtendedCapture.Species)

	assert.Equal(t, []string{"Corvus corax"}, src.Realtime.Species.Include)
	assert.Equal(t, []string{"Passer domesticus"}, src.Realtime.Species.Exclude)
	sc, ok := src.Realtime.Species.Config["Turdus merula"]
	require.True(t, ok)
	assert.InDelta(t, 0.5, sc.Threshold, 0.0001)
	require.Len(t, sc.Actions, 1)
	assert.Equal(t, []string{"hi"}, sc.Actions[0].Parameters)
	_, hasMutated := src.Realtime.Species.Config[mutated]
	assert.False(t, hasMutated, "species config on src must not contain the mutated key")

	assert.Equal(t, map[string]Season{"winter": {StartMonth: 12, StartDay: 1}},
		src.Realtime.SpeciesTracking.SeasonalTracking.Seasons)

	require.Len(t, src.Security.OAuthProviders, 1)
	assert.Equal(t, []string{"openid", "email"}, src.Security.OAuthProviders[0].Scopes)

	require.Len(t, src.Backup.Targets, 1)
	assert.Equal(t, "/backups", src.Backup.Targets[0].Settings["path"])
	// Nested slice value through any survived the clone without leaking
	// the mutation from dst back to src.
	assert.Equal(t, []any{"--archive", "--compress"}, src.Backup.Targets[0].Settings["options"],
		"nested []any inside Settings must be independently cloned")
	// Nested map + its nested slice both stay pristine.
	ssh, ok := src.Backup.Targets[0].Settings["ssh"].(map[string]any)
	require.True(t, ok, "ssh sub-map must still be a map[string]any")
	assert.Equal(t, "/home/user/.ssh/id_rsa", ssh["keyPath"],
		"nested map[string]any inside Settings must be independently cloned")
	assert.Equal(t, []any{22, 2222}, ssh["ports"],
		"deeply nested []any inside nested map must be independently cloned")
	require.Len(t, src.Backup.Schedules, 1)
	assert.Equal(t, 3, src.Backup.Schedules[0].Hour)

	require.Len(t, src.Notification.Push.Providers, 1)
	pp := src.Notification.Push.Providers[0]
	assert.Equal(t, []string{"https://hook.example/api"}, pp.URLs)
	assert.Equal(t, []string{"--flag"}, pp.Args)
	assert.Equal(t, map[string]string{"API_KEY": "secret"}, pp.Environment)
	require.Len(t, pp.Endpoints, 1)
	assert.Equal(t, "https://a", pp.Endpoints[0].URL)
	assert.Equal(t, map[string]string{"X-Custom": "1"}, pp.Endpoints[0].Headers)
	assert.Equal(t, []string{"new_species"}, pp.Filter.Types)
	assert.Equal(t, []string{"high"}, pp.Filter.Priorities)
	assert.Equal(t, []string{"detection"}, pp.Filter.Components)
	assert.Equal(t, "garden", pp.Filter.MetadataFilters["location"])
	assert.Equal(t, []any{"urgent", "bird-of-prey"}, pp.Filter.MetadataFilters["tags"],
		"nested []any inside MetadataFilters must be independently cloned")
}
