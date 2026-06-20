package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

type hotReloadCategory string

const (
	hotReloadDisplay hotReloadCategory = "display"
	hotReloadFresh   hotReloadCategory = "fresh"
	hotReloadRestart hotReloadCategory = "restart"
	hotReloadRuntime hotReloadCategory = "runtime"
	hotReloadNotify  hotReloadCategory = "notify"
)

type hotReloadEntry struct {
	categories []hotReloadCategory
	action     string
	todoAction string
}

// hotReloadRegistry maps dot-separated field paths to their reload behavior.
// Parent entries cover all children; field-level entries fully replace parent.
var hotReloadRegistry = map[string]hotReloadEntry{
	// --- Top-level ---
	"Debug": {categories: []hotReloadCategory{hotReloadFresh}},

	// --- Runtime values (yaml:"-") ---
	"Version":            {categories: []hotReloadCategory{hotReloadRuntime}},
	"BuildDate":          {categories: []hotReloadCategory{hotReloadRuntime}},
	"SystemID":           {categories: []hotReloadCategory{hotReloadRuntime}},
	"ValidationWarnings": {categories: []hotReloadCategory{hotReloadRuntime}},

	// --- Logging ---
	"Logging": {categories: []hotReloadCategory{hotReloadRestart}},

	// --- Main ---
	"Main": {categories: []hotReloadCategory{hotReloadDisplay}},

	// --- BirdNET ---
	"BirdNET.Debug":       {categories: []hotReloadCategory{hotReloadFresh}},
	"BirdNET.Sensitivity": {categories: []hotReloadCategory{hotReloadFresh}},
	"BirdNET.Threshold": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "recalculate_dynamic_thresholds",
	},
	"BirdNET.Overlap":            {categories: []hotReloadCategory{hotReloadFresh}},
	"BirdNET.Longitude":          {categories: []hotReloadCategory{hotReloadDisplay}, action: "rebuild_range_filter"},
	"BirdNET.Latitude":           {categories: []hotReloadCategory{hotReloadDisplay}, action: "rebuild_range_filter"},
	"BirdNET.LocationConfigured": {categories: []hotReloadCategory{hotReloadDisplay}},
	"BirdNET.Threads":            {categories: []hotReloadCategory{hotReloadFresh}, action: "reload_birdnet"},
	"BirdNET.Locale":             {categories: []hotReloadCategory{hotReloadDisplay}, action: "reload_birdnet"},
	"BirdNET.RangeFilter":        {categories: []hotReloadCategory{hotReloadFresh}, action: "rebuild_range_filter"},
	"BirdNET.ModelPath":          {categories: []hotReloadCategory{hotReloadFresh}, action: "reload_birdnet"},
	"BirdNET.LabelPath":          {categories: []hotReloadCategory{hotReloadFresh}, action: "reload_birdnet"},
	"BirdNET.Labels":             {categories: []hotReloadCategory{hotReloadRuntime}},
	"BirdNET.UseXNNPACK":         {categories: []hotReloadCategory{hotReloadFresh}, action: "reload_birdnet"},
	"BirdNET.ONNXRuntimePath":    {categories: []hotReloadCategory{hotReloadRestart}},
	"BirdNET.Version":            {categories: []hotReloadCategory{hotReloadFresh}, action: "reload_birdnet"},

	// --- Perch ---
	"Perch": {categories: []hotReloadCategory{hotReloadRestart}},

	// --- Bat ---
	"Bat": {categories: []hotReloadCategory{hotReloadFresh}},

	// --- BSG ---
	"BSG": {categories: []hotReloadCategory{hotReloadRestart}},

	// --- Models ---
	"Models": {categories: []hotReloadCategory{hotReloadRestart}},

	// --- LowMemory (applied once at startup: mallopt before threads, GOMEMLIMIT) ---
	"LowMemory": {categories: []hotReloadCategory{hotReloadRestart}},

	// --- TaxonomySynonyms ---
	"TaxonomySynonyms": {categories: []hotReloadCategory{hotReloadFresh}},

	// --- Input (runtime only, yaml:"-") ---
	"Input": {categories: []hotReloadCategory{hotReloadRuntime}},

	// --- Realtime ---
	"Realtime.Interval": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "update_detection_intervals",
	},
	"Realtime.ProcessingTime": {categories: []hotReloadCategory{hotReloadFresh}},

	// -- Audio --
	"Realtime.Audio.Sources.*.Name":       {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_audio_sources"},
	"Realtime.Audio.Sources.*.Device":     {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_audio_sources"},
	"Realtime.Audio.Sources.*.SampleRate": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_audio_sources"},
	"Realtime.Audio.Sources.*.Gain":       {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_audio_sources"},
	"Realtime.Audio.Sources.*.Model":      {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_audio_sources"},
	"Realtime.Audio.Sources.*.Models":     {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_audio_sources"},
	"Realtime.Audio.Sources.*.Equalizer":  {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.Audio.Sources.*.QuietHours": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_quiet_hours"},
	"Realtime.Audio.Source":               {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.Audio.FfmpegPath":           {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.Audio.FfmpegVersion":        {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.Audio.FfmpegMajor":          {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.Audio.FfmpegMinor":          {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.Audio.SoxPath":              {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.Audio.SoxAudioTypes":        {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.Audio.FfprobePath":          {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.Audio.StreamTransport":      {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.Audio.Export":               {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.Audio.SoundLevel":           {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_sound_level"},
	"Realtime.Audio.Equalizer":            {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.Audio.QuietHours":           {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_quiet_hours"},
	"Realtime.Audio.Watchdog":             {categories: []hotReloadCategory{hotReloadRestart}},

	// -- Dashboard (display only, read per-request by frontend) --
	"Realtime.Dashboard": {categories: []hotReloadCategory{hotReloadDisplay}},

	// SpeciesGuide rebuilds its provider cache live via the
	// reconfigure_species_guide control signal (field-level entry covers the subtree).
	"Realtime.Dashboard.SpeciesGuide": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "reconfigure_species_guide",
	},

	// -- DynamicThreshold --
	"Realtime.DynamicThreshold.Enabled": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "reconfigure_dynamic_thresholds",
	},
	"Realtime.DynamicThreshold.Debug":      {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.DynamicThreshold.Trigger":    {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.DynamicThreshold.Min":        {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.DynamicThreshold.ValidHours": {categories: []hotReloadCategory{hotReloadFresh}},

	// -- FalsePositiveFilter --
	"Realtime.FalsePositiveFilter": {categories: []hotReloadCategory{hotReloadFresh}},

	// -- Log (OBS) --
	"Realtime.Log": {categories: []hotReloadCategory{hotReloadFresh}},

	// -- LogDeduplication --
	"Realtime.LogDeduplication": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "reconfigure_log_deduplication",
	},

	// -- Birdweather --
	"Realtime.Birdweather": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_birdweather"},

	// -- eBird --
	"Realtime.EBird": {categories: []hotReloadCategory{hotReloadFresh}},

	// -- OpenWeather (runtime, yaml:"-") --
	"Realtime.OpenWeather": {categories: []hotReloadCategory{hotReloadRuntime}},

	// -- Filters --
	"Realtime.PrivacyFilter":  {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.DogBarkFilter":  {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.DaylightFilter": {categories: []hotReloadCategory{hotReloadFresh}},

	// -- RTSP --
	"Realtime.RTSP.Streams.*.Name":        {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_rtsp_sources"},
	"Realtime.RTSP.Streams.*.URL":         {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_rtsp_sources"},
	"Realtime.RTSP.Streams.*.Enabled":     {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_rtsp_sources"},
	"Realtime.RTSP.Streams.*.Type":        {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_rtsp_sources"},
	"Realtime.RTSP.Streams.*.Transport":   {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_rtsp_sources"},
	"Realtime.RTSP.Streams.*.ChannelMode": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_rtsp_sources"},
	"Realtime.RTSP.Streams.*.Equalizer":   {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.RTSP.Streams.*.QuietHours":  {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_quiet_hours"},
	"Realtime.RTSP.Streams.*.Models":      {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_rtsp_sources"},
	"Realtime.RTSP.URLs":                  {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.RTSP.Transport":             {categories: []hotReloadCategory{hotReloadRuntime}},
	"Realtime.RTSP.Health": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "reconfigure_rtsp_health",
	},
	"Realtime.RTSP.FFmpegParameters": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_rtsp_sources"},

	// -- MQTT --
	"Realtime.MQTT.Enabled":       {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_mqtt"},
	"Realtime.MQTT.Debug":         {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.MQTT.Broker":        {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_mqtt"},
	"Realtime.MQTT.Topic":         {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_mqtt"},
	"Realtime.MQTT.Username":      {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_mqtt"},
	"Realtime.MQTT.Password":      {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_mqtt"},
	"Realtime.MQTT.Retain":        {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.MQTT.RetrySettings": {categories: []hotReloadCategory{hotReloadFresh}},
	"Realtime.MQTT.TLS":           {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_mqtt"},
	"Realtime.MQTT.HomeAssistant": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "reconfigure_mqtt",
	},

	// -- Telemetry --
	"Realtime.Telemetry": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_telemetry"},

	// -- Monitoring --
	"Realtime.Monitoring": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "reconfigure_monitoring",
	},

	// -- Species --
	"Realtime.Species": {categories: []hotReloadCategory{hotReloadFresh}, action: "update_detection_intervals"},

	// -- Weather --
	"Realtime.Weather": {categories: []hotReloadCategory{hotReloadFresh}},

	// -- SpeciesTracking --
	"Realtime.SpeciesTracking": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_species_tracking"},

	// -- ExtendedCapture --
	"Realtime.ExtendedCapture.Enabled":              {categories: []hotReloadCategory{hotReloadFresh}, action: "rebuild_extended_capture"},
	"Realtime.ExtendedCapture.MaxDuration":          {categories: []hotReloadCategory{hotReloadFresh}, action: "rebuild_extended_capture"},
	"Realtime.ExtendedCapture.CaptureBufferSeconds": {categories: []hotReloadCategory{hotReloadNotify}},
	"Realtime.ExtendedCapture.Species":              {categories: []hotReloadCategory{hotReloadFresh}, action: "rebuild_extended_capture"},

	// --- WebServer ---
	"WebServer.Debug":          {categories: []hotReloadCategory{hotReloadFresh}},
	"WebServer.Enabled":        {categories: []hotReloadCategory{hotReloadNotify}},
	"WebServer.Port":           {categories: []hotReloadCategory{hotReloadNotify}},
	"WebServer.BasePath":       {categories: []hotReloadCategory{hotReloadNotify}},
	"WebServer.AllowEmbedding": {categories: []hotReloadCategory{hotReloadFresh}},
	"WebServer.LiveStream": {
		categories: []hotReloadCategory{hotReloadFresh},
		action:     "reconfigure_livestream",
	},
	"WebServer.EnableTerminal": {categories: []hotReloadCategory{hotReloadNotify}},

	// --- Security ---
	"Security": {categories: []hotReloadCategory{hotReloadFresh}},

	// --- Sentry ---
	"Sentry": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_telemetry"},

	// --- Output ---
	"Output": {categories: []hotReloadCategory{hotReloadRestart}},

	// --- Backup ---
	"Backup": {categories: []hotReloadCategory{hotReloadFresh}},

	// --- Notification ---
	"Notification": {categories: []hotReloadCategory{hotReloadFresh}, action: "reconfigure_push_notifications"},

	// --- Alerting ---
	"Alerting": {categories: []hotReloadCategory{hotReloadFresh}},
}

// Ceiling decreases as TODO actions are implemented; target is zero.
const maxTodoActions = 0

// TestSettingsHotReloadCoverage ensures every leaf field in conf.Settings has
// a declared hot-reload behavior in the registry. Fails CI when a new field
// is added without declaring how it applies at runtime.
func TestSettingsHotReloadCoverage(t *testing.T) {
	t.Parallel()

	settingsType := reflect.TypeFor[conf.Settings]()
	visited := make(map[reflect.Type]bool)
	var uncovered []string

	walkSettingsType(settingsType, "", visited, &uncovered)

	require.Empty(t, uncovered,
		"found %d settings fields without hot-reload declarations; add entries to hotReloadRegistry",
		len(uncovered))
}

// TestHotReloadRegistryActionsExist verifies every action: string in the registry
// maps to an entry in settingsChangeChecks or AudioSettingsChangeActions.
func TestHotReloadRegistryActionsExist(t *testing.T) {
	t.Parallel()

	tableActions := SettingsChangeActions()
	audioActions := AudioSettingsChangeActions()
	allActions := make(map[string]bool, len(tableActions)+len(audioActions))
	for _, a := range tableActions {
		allActions[a] = true
	}
	for _, a := range audioActions {
		allActions[a] = true
	}

	for path, entry := range hotReloadRegistry {
		if entry.action == "" {
			continue
		}
		assert.True(t, allActions[entry.action],
			"registry path %q declares action %q which does not exist in detector tables",
			path, entry.action)
	}
}

// TestDetectorTableActionsReferenced verifies every non-empty action in the
// settingsChangeChecks table is referenced by at least one registry entry.
func TestDetectorTableActionsReferenced(t *testing.T) {
	t.Parallel()

	registryActions := make(map[string]bool, len(hotReloadRegistry))
	for _, entry := range hotReloadRegistry {
		if entry.action != "" {
			registryActions[entry.action] = true
		}
		if entry.todoAction != "" {
			registryActions[entry.todoAction] = true
		}
	}

	for _, a := range SettingsChangeActions() {
		assert.True(t, registryActions[a],
			"detector table action %q is not referenced by any registry entry", a)
	}
	for _, a := range AudioSettingsChangeActions() {
		assert.True(t, registryActions[a],
			"audio detector action %q is not referenced by any registry entry", a)
	}
}

// TestTodoActionCeiling asserts the number of TODO gaps trends toward zero.
func TestTodoActionCeiling(t *testing.T) {
	t.Parallel()

	var todoCount int
	for _, entry := range hotReloadRegistry {
		if entry.todoAction != "" {
			todoCount++
		}
	}
	t.Logf("current TODO:action count: %d (ceiling: %d)", todoCount, maxTodoActions)
	assert.LessOrEqual(t, todoCount, maxTodoActions,
		"TODO:action count %d exceeds ceiling %d; implement detectors or raise ceiling intentionally",
		todoCount, maxTodoActions)
}

func walkSettingsType(t reflect.Type, prefix string, visited map[reflect.Type]bool, uncovered *[]string) {
	t = unwrapPtr(t)
	if t.Kind() != reflect.Struct {
		return
	}
	if visited[t] {
		return
	}
	visited[t] = true
	defer func() { visited[t] = false }()

	for field := range t.Fields() {
		f := field // capture for addressability
		if !f.IsExported() {
			continue
		}

		if isYAMLExcluded(&f) {
			continue
		}

		if f.Anonymous {
			walkSettingsType(f.Type, prefix, visited, uncovered)
			continue
		}

		var path string
		if prefix == "" {
			path = f.Name
		} else {
			path = prefix + "." + f.Name
		}

		if lookupRegistry(path) {
			continue
		}

		fieldType := unwrapPtr(f.Type)

		switch fieldType.Kind() {
		case reflect.Struct:
			walkSettingsType(fieldType, path, visited, uncovered)
		case reflect.Slice, reflect.Array:
			elemType := unwrapPtr(fieldType.Elem())
			if elemType.Kind() == reflect.Struct && isProjectType(elemType) {
				walkSettingsType(elemType, path+".*", visited, uncovered)
			} else {
				*uncovered = append(*uncovered, path)
			}
		case reflect.Map:
			elemType := unwrapPtr(fieldType.Elem())
			if elemType.Kind() == reflect.Struct && isProjectType(elemType) {
				walkSettingsType(elemType, path+".*", visited, uncovered)
			} else {
				*uncovered = append(*uncovered, path)
			}
		default:
			*uncovered = append(*uncovered, path)
		}
	}
}

func lookupRegistry(path string) bool {
	if _, ok := hotReloadRegistry[path]; ok {
		return true
	}
	// Walk up parent paths
	for {
		idx := strings.LastIndex(path, ".")
		if idx < 0 {
			break
		}
		path = path[:idx]
		if _, ok := hotReloadRegistry[path]; ok {
			return true
		}
	}
	return false
}

func unwrapPtr(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

func isYAMLExcluded(field *reflect.StructField) bool {
	tag := field.Tag.Get("yaml")
	return tag == "-"
}

func isProjectType(t reflect.Type) bool {
	pkg := t.PkgPath()
	if pkg == "" {
		return true // anonymous struct
	}
	return strings.HasPrefix(pkg, "github.com/tphakala/birdnet-go")
}
