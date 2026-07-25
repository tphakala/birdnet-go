package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Coverage of getBlockedFieldMap is split across the three tests in this file
// because the leaves are not all reachable the same way:
//
//   - TestPatchCannotChangeBlockedFields drives real PATCH requests. It covers
//     every blocked leaf a client can actually put in a request body, and each
//     case fails if restoreBlockedFields stops enforcing.
//   - TestRestoreBlockedFieldsCoversEveryLeaf drives restoreBlockedFields
//     directly. It covers ALL leaves, including the ones PATCH cannot reach, and
//     is the test that fails if a leaf is dropped from the map or the walk.
//   - TestBlockedFieldMapNamesRealFields pins that every name in the map still
//     resolves to a struct field, so a rename cannot silently un-block a field.
//
// Some leaves are also tagged json:"-" (BirdNET.Labels, Realtime.Audio.SoxAudioTypes,
// Input), which independently keeps the JSON merge away from them. A PATCH-level
// immutability assertion on those would pass with enforcement removed, so they
// are deliberately covered by the direct test instead of the HTTP one rather
// than being listed in both and looking better covered than they are.

// skippedFieldsOf decodes the skippedFields list from a settings-section
// response.
func skippedFieldsOf(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var resp struct {
		SkippedFields []string `json:"skippedFields"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp.SkippedFields
}

// TestPatchCannotChangeBlockedFields is the regression test for the enforcement
// gap: handleGenericSection merged the request straight into the section struct
// and then only appended a note to skippedFields saying restrictions existed, so
// every field getBlockedFieldMap marks never-settable was settable via PATCH.
//
// Each case sends a value different from the seeded one, so each fails if
// restoreBlockedFields is removed from UpdateSectionSettings.
func TestPatchCannotChangeBlockedFields(t *testing.T) {
	seededTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		section string
		seed    func(*conf.Settings)
		body    map[string]any
		verify  func(*testing.T, *conf.Settings)
	}{
		{
			name:    "Security.SessionSecret",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.SessionSecret = "server-generated-secret" },
			body:    map[string]any{"sessionSecret": "attacker-pinned-secret"},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "server-generated-secret", s.Security.SessionSecret)
			},
		},
		{
			name:    "Security.SessionDuration",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.SessionDuration = 168 * time.Hour },
			body:    map[string]any{"sessionDuration": int64(100 * 365 * 24 * time.Hour)},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, 168*time.Hour, s.Security.SessionDuration)
			},
		},
		{
			name:    "Security.BasicAuth.ClientID",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.ClientID = "oauth-client-id" },
			body:    map[string]any{"basicAuth": map[string]any{"clientId": "attacker-client-id"}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "oauth-client-id", s.Security.BasicAuth.ClientID)
			},
		},
		{
			name:    "Security.BasicAuth.ClientSecret",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.ClientSecret = "oauth-client-secret" },
			body:    map[string]any{"basicAuth": map[string]any{"clientSecret": "attacker-client-secret"}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "oauth-client-secret", s.Security.BasicAuth.ClientSecret)
			},
		},
		{
			name:    "Security.BasicAuth.AuthCodeExp",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.AuthCodeExp = 10 * time.Minute },
			body:    map[string]any{"basicAuth": map[string]any{"authCodeExp": int64(365 * 24 * time.Hour)}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, 10*time.Minute, s.Security.BasicAuth.AuthCodeExp)
			},
		},
		{
			name:    "Security.BasicAuth.AccessTokenExp",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.AccessTokenExp = time.Hour },
			body:    map[string]any{"basicAuth": map[string]any{"accessTokenExp": int64(365 * 24 * time.Hour)}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, time.Hour, s.Security.BasicAuth.AccessTokenExp)
			},
		},
		{
			name:    "Diagnostics.Profiling.Token",
			section: "diagnostics",
			seed:    func(s *conf.Settings) { s.Diagnostics.Profiling.Token = "server-minted-token" },
			body:    map[string]any{"profiling": map[string]any{"token": "attacker-known-token"}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "server-minted-token", s.Diagnostics.Profiling.Token)
			},
		},
		{
			name:    "BirdNET.RangeFilter.Model",
			section: "birdnet",
			seed:    func(s *conf.Settings) { s.BirdNET.RangeFilter.Model = "latest" },
			body:    map[string]any{"rangeFilter": map[string]any{"model": "legacy"}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "latest", s.BirdNET.RangeFilter.Model)
			},
		},
		{
			name:    "BirdNET.RangeFilter.Species",
			section: "birdnet",
			seed: func(s *conf.Settings) {
				s.BirdNET.RangeFilter.Species = []string{"Turdus merula", "Parus major"}
			},
			body: map[string]any{"rangeFilter": map[string]any{"species": []string{"Corvus corax"}}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, []string{"Turdus merula", "Parus major"}, s.BirdNET.RangeFilter.Species)
			},
		},
		{
			name:    "BirdNET.RangeFilter.LastUpdated",
			section: "birdnet",
			seed:    func(s *conf.Settings) { s.BirdNET.RangeFilter.LastUpdated = seededTime },
			body: map[string]any{
				"rangeFilter": map[string]any{"lastUpdated": "2000-01-01T00:00:00Z"},
			},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.True(t, seededTime.Equal(s.BirdNET.RangeFilter.LastUpdated),
					"want %s, got %s", seededTime, s.BirdNET.RangeFilter.LastUpdated)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)
			tt.seed(controller.Settings.Load())

			patchSection(t, e, controller, tt.section, tt.body)
			tt.verify(t, controller.Settings.Load())
		})
	}
}

// TestPatchEnforcesBlockedAudioToolPaths covers Realtime.Audio.FfmpegPath and
// SoxPath, which the table above deliberately leaves out.
//
// Those two cannot be covered there by asserting the stored value, for two
// reasons. conf.validateAudioSettings runs after the merge and re-resolves both
// paths through ValidateToolPath, so a nonexistent attacker path is discarded
// even with enforcement removed (the assertion would pass for the wrong reason),
// while a path that does exist makes the expected result depend on whether the
// machine running the test has ffmpeg installed and where.
//
// Asserting on skippedFields instead is immune to both: restoreBlockedFields
// runs before conf.ValidateSettings and reports what it reverted, and nothing
// else in the request path can populate that list. The stored-value guarantee
// for these two leaves is covered by TestRestoreBlockedFieldsCoversEveryLeaf.
//
// Both routes to the field are exercised. PATCH /settings/audio targets
// Realtime.Audio directly, so its section name is "audio" while the blocked map
// reaches the same fields under "Realtime" — a per-section lookup keyed on the
// section name would have missed exactly this case.
func TestPatchEnforcesBlockedAudioToolPaths(t *testing.T) {
	tests := []struct {
		name    string
		section string
		body    map[string]any
		want    []string
	}{
		{
			name:    "audio section",
			section: "audio",
			body: map[string]any{
				"ffmpegPath": "/tmp/attacker/ffmpeg",
				"soxPath":    "/tmp/attacker/sox",
			},
			want: []string{"Realtime.Audio.FfmpegPath", "Realtime.Audio.SoxPath"},
		},
		{
			name:    "realtime section",
			section: "realtime",
			body: map[string]any{
				"audio": map[string]any{"ffmpegPath": "/tmp/attacker/ffmpeg"},
			},
			want: []string{"Realtime.Audio.FfmpegPath"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)
			settings := controller.Settings.Load()
			settings.Realtime.Audio.FfmpegPath = "/usr/bin/ffmpeg"
			settings.Realtime.Audio.SoxPath = "/usr/bin/sox"

			rec := patchSection(t, e, controller, tt.section, tt.body)

			assert.Equal(t, tt.want, skippedFieldsOf(t, rec),
				"the blocked audio tool paths must be rejected and reported")
		})
	}
}

// TestPatchStillWritesUnblockedNeighbours guards the opposite failure: an
// enforcement pass that reverted the whole section instead of the blocked leaves
// would satisfy every assertion above while breaking the settings API. Each
// field here sits directly beside a blocked one.
func TestPatchStillWritesUnblockedNeighbours(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	settings := controller.Settings.Load()
	settings.Security.SessionSecret = "server-generated-secret"
	settings.Security.BasicAuth.ClientSecret = "oauth-client-secret"

	patchSection(t, e, controller, "security", map[string]any{
		"host":          "birds.example.com",
		"sessionSecret": "attacker-pinned-secret",
		"basicAuth": map[string]any{
			"enabled":      true,
			"clientSecret": "attacker-client-secret",
		},
	})

	updated := controller.Settings.Load()
	assert.Equal(t, "birds.example.com", updated.Security.Host,
		"an unblocked sibling of a blocked field must still be writable")
	assert.True(t, updated.Security.BasicAuth.Enabled,
		"an unblocked sibling inside a blocked-field struct must still be writable")
	assert.Equal(t, "server-generated-secret", updated.Security.SessionSecret)
	assert.Equal(t, "oauth-client-secret", updated.Security.BasicAuth.ClientSecret)
}

// TestPatchReportsBlockedFieldsItRejected pins the response contract. The
// skippedFields list used to be the boilerplate string "Section security has
// field-level restrictions" emitted whether or not anything was rejected; it now
// names the paths actually reverted, and stays empty when nothing was.
func TestPatchReportsBlockedFieldsItRejected(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Load().Security.SessionSecret = "server-generated-secret"

	rejected := patchSection(t, e, controller, "security",
		map[string]any{"sessionSecret": "attacker-pinned-secret"})
	assert.Equal(t, []string{"Security.SessionSecret"}, skippedFieldsOf(t, rejected),
		"a rejected field must be named in the response")

	clean := patchSection(t, e, controller, "security",
		map[string]any{"host": "birds.example.com"})
	assert.Empty(t, skippedFieldsOf(t, clean),
		"a request that touches nothing blocked must report nothing skipped")
}

// TestRestoreBlockedFieldsCoversEveryLeaf drives the enforcement function
// directly, so it covers the leaves PATCH cannot reach: the top-level runtime
// fields (no section maps to the root struct) and the json:"-" fields the merge
// never sees. Deleting any leaf from getBlockedFieldMap, or any arm from the
// walk, fails the matching case here.
func TestRestoreBlockedFieldsCoversEveryLeaf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		seed    func(*conf.Settings)
		tamper  func(*conf.Settings)
		current func(*conf.Settings) any
	}{
		{
			name:    "Version",
			path:    "Version",
			seed:    func(s *conf.Settings) { s.Version = "v1.2.3" },
			tamper:  func(s *conf.Settings) { s.Version = "v9.9.9" },
			current: func(s *conf.Settings) any { return s.Version },
		},
		{
			name:    "BuildDate",
			path:    "BuildDate",
			seed:    func(s *conf.Settings) { s.BuildDate = "2026-03-01" },
			tamper:  func(s *conf.Settings) { s.BuildDate = "1999-01-01" },
			current: func(s *conf.Settings) any { return s.BuildDate },
		},
		{
			name:    "SystemID",
			path:    "SystemID",
			seed:    func(s *conf.Settings) { s.SystemID = "real-system-id" },
			tamper:  func(s *conf.Settings) { s.SystemID = "spoofed-system-id" },
			current: func(s *conf.Settings) any { return s.SystemID },
		},
		{
			name:    "ValidationWarnings",
			path:    "ValidationWarnings",
			seed:    func(s *conf.Settings) { s.ValidationWarnings = []string{"real warning"} },
			tamper:  func(s *conf.Settings) { s.ValidationWarnings = []string{"injected"} },
			current: func(s *conf.Settings) any { return s.ValidationWarnings },
		},
		{
			name:    "Input",
			path:    "Input",
			seed:    func(s *conf.Settings) { s.Input.Path = "/data/clips" },
			tamper:  func(s *conf.Settings) { s.Input.Path = "/etc"; s.Input.Recursive = true },
			current: func(s *conf.Settings) any { return s.Input },
		},
		{
			name:    "BirdNET.Labels",
			path:    "BirdNET.Labels",
			seed:    func(s *conf.Settings) { s.BirdNET.Labels = []string{"Turdus merula_Eurasian Blackbird"} },
			tamper:  func(s *conf.Settings) { s.BirdNET.Labels = []string{"injected_Injected Bird"} },
			current: func(s *conf.Settings) any { return s.BirdNET.Labels },
		},
		{
			name:    "BirdNET.RangeFilter.Model",
			path:    "BirdNET.RangeFilter.Model",
			seed:    func(s *conf.Settings) { s.BirdNET.RangeFilter.Model = "latest" },
			tamper:  func(s *conf.Settings) { s.BirdNET.RangeFilter.Model = "legacy" },
			current: func(s *conf.Settings) any { return s.BirdNET.RangeFilter.Model },
		},
		{
			name:    "BirdNET.RangeFilter.Species",
			path:    "BirdNET.RangeFilter.Species",
			seed:    func(s *conf.Settings) { s.BirdNET.RangeFilter.Species = []string{"Turdus merula"} },
			tamper:  func(s *conf.Settings) { s.BirdNET.RangeFilter.Species = []string{"Corvus corax"} },
			current: func(s *conf.Settings) any { return s.BirdNET.RangeFilter.Species },
		},
		{
			name: "BirdNET.RangeFilter.LastUpdated",
			path: "BirdNET.RangeFilter.LastUpdated",
			seed: func(s *conf.Settings) {
				s.BirdNET.RangeFilter.LastUpdated = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
			},
			tamper: func(s *conf.Settings) {
				s.BirdNET.RangeFilter.LastUpdated = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			},
			current: func(s *conf.Settings) any { return s.BirdNET.RangeFilter.LastUpdated },
		},
		{
			name:    "Security.SessionSecret",
			path:    "Security.SessionSecret",
			seed:    func(s *conf.Settings) { s.Security.SessionSecret = "server-generated-secret" },
			tamper:  func(s *conf.Settings) { s.Security.SessionSecret = "attacker-pinned-secret" },
			current: func(s *conf.Settings) any { return s.Security.SessionSecret },
		},
		{
			name:    "Security.SessionDuration",
			path:    "Security.SessionDuration",
			seed:    func(s *conf.Settings) { s.Security.SessionDuration = 168 * time.Hour },
			tamper:  func(s *conf.Settings) { s.Security.SessionDuration = 87600 * time.Hour },
			current: func(s *conf.Settings) any { return s.Security.SessionDuration },
		},
		{
			name:    "Security.BasicAuth.ClientID",
			path:    "Security.BasicAuth.ClientID",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.ClientID = "oauth-client-id" },
			tamper:  func(s *conf.Settings) { s.Security.BasicAuth.ClientID = "attacker-client-id" },
			current: func(s *conf.Settings) any { return s.Security.BasicAuth.ClientID },
		},
		{
			name:    "Security.BasicAuth.ClientSecret",
			path:    "Security.BasicAuth.ClientSecret",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.ClientSecret = "oauth-client-secret" },
			tamper:  func(s *conf.Settings) { s.Security.BasicAuth.ClientSecret = "attacker-client-secret" },
			current: func(s *conf.Settings) any { return s.Security.BasicAuth.ClientSecret },
		},
		{
			name:    "Security.BasicAuth.AuthCodeExp",
			path:    "Security.BasicAuth.AuthCodeExp",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.AuthCodeExp = 10 * time.Minute },
			tamper:  func(s *conf.Settings) { s.Security.BasicAuth.AuthCodeExp = 8760 * time.Hour },
			current: func(s *conf.Settings) any { return s.Security.BasicAuth.AuthCodeExp },
		},
		{
			name:    "Security.BasicAuth.AccessTokenExp",
			path:    "Security.BasicAuth.AccessTokenExp",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.AccessTokenExp = time.Hour },
			tamper:  func(s *conf.Settings) { s.Security.BasicAuth.AccessTokenExp = 8760 * time.Hour },
			current: func(s *conf.Settings) any { return s.Security.BasicAuth.AccessTokenExp },
		},
		{
			name:    "Diagnostics.Profiling.Token",
			path:    "Diagnostics.Profiling.Token",
			seed:    func(s *conf.Settings) { s.Diagnostics.Profiling.Token = "server-minted-token" },
			tamper:  func(s *conf.Settings) { s.Diagnostics.Profiling.Token = "attacker-known-token" },
			current: func(s *conf.Settings) any { return s.Diagnostics.Profiling.Token },
		},
		{
			name:    "Realtime.Audio.FfmpegPath",
			path:    "Realtime.Audio.FfmpegPath",
			seed:    func(s *conf.Settings) { s.Realtime.Audio.FfmpegPath = "/usr/bin/ffmpeg" },
			tamper:  func(s *conf.Settings) { s.Realtime.Audio.FfmpegPath = "/tmp/attacker/ffmpeg" },
			current: func(s *conf.Settings) any { return s.Realtime.Audio.FfmpegPath },
		},
		{
			name:    "Realtime.Audio.SoxPath",
			path:    "Realtime.Audio.SoxPath",
			seed:    func(s *conf.Settings) { s.Realtime.Audio.SoxPath = "/usr/bin/sox" },
			tamper:  func(s *conf.Settings) { s.Realtime.Audio.SoxPath = "/tmp/attacker/sox" },
			current: func(s *conf.Settings) any { return s.Realtime.Audio.SoxPath },
		},
		{
			name:    "Realtime.Audio.SoxAudioTypes",
			path:    "Realtime.Audio.SoxAudioTypes",
			seed:    func(s *conf.Settings) { s.Realtime.Audio.SoxAudioTypes = []string{"wav", "flac"} },
			tamper:  func(s *conf.Settings) { s.Realtime.Audio.SoxAudioTypes = []string{"injected"} },
			current: func(s *conf.Settings) any { return s.Realtime.Audio.SoxAudioTypes },
		},
	}

	// Every leaf in the map must appear above. Without this the table can drift
	// out of sync with getBlockedFieldMap and still look exhaustive.
	covered := make(map[string]bool, len(tests))
	for _, tt := range tests {
		covered[tt.path] = true
	}
	for _, leaf := range blockedLeafPaths(getBlockedFieldMap(), "") {
		assert.True(t, covered[leaf], "blocked leaf %s has no case in this table", leaf)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			current := &conf.Settings{}
			tt.seed(current)

			updated := conf.CloneSettings(current)
			tt.tamper(updated)
			require.NotEqual(t, tt.current(current), tt.current(updated),
				"tamper must actually change the field, or the case proves nothing")

			restored := restoreBlockedFields(current, updated)

			assert.Equal(t, tt.current(current), tt.current(updated),
				"blocked field was not restored to its pre-update value")
			assert.Contains(t, restored, tt.path,
				"a restored field must be reported so the response and log can name it")
		})
	}
}

// TestRestoreBlockedFieldsIsQuietWhenNothingChanged pins that an ordinary save
// reports nothing, at the level of restoreBlockedFields itself. An
// implementation that restored (and reported) every leaf unconditionally would
// still satisfy every immutability assertion in this file while logging a
// blocked-field warning on every settings write; this and the second half of
// TestPatchReportsBlockedFieldsItRejected are what reject it.
//
// The LastUpdated case is the one that actually bites: without the time.Time
// arm in blockedValuesEqual, every PATCH of the birdnet section reports a
// phantom rejection.
func TestRestoreBlockedFieldsIsQuietWhenNothingChanged(t *testing.T) {
	t.Parallel()

	current := &conf.Settings{}
	current.Security.SessionSecret = "server-generated-secret"
	current.BirdNET.Labels = []string{"Turdus merula_Eurasian Blackbird"}
	// A monotonic-clock reading, as conf.updateIncludedSpecies leaves behind.
	current.BirdNET.RangeFilter.LastUpdated = time.Now()

	updated := conf.CloneSettings(current)
	updated.Main.Name = "renamed node"
	// The JSON round trip the PATCH merge performs drops the monotonic reading
	// without changing the instant. That must not read as a blocked-field change.
	updated.BirdNET.RangeFilter.LastUpdated = current.BirdNET.RangeFilter.LastUpdated.Round(0)

	assert.Empty(t, restoreBlockedFields(current, updated),
		"an update that touches nothing blocked must report nothing")
	assert.Equal(t, "renamed node", updated.Main.Name,
		"restoring blocked fields must not undo an unblocked change")
}

// TestRestoreBlockedFieldsDoesNotAliasCurrent pins that a restored slice is
// copied rather than shared with the outgoing snapshot, matching what
// conf.CloneSettings guarantees for the clone it hands out.
func TestRestoreBlockedFieldsDoesNotAliasCurrent(t *testing.T) {
	t.Parallel()

	current := &conf.Settings{}
	current.BirdNET.RangeFilter.Species = []string{"Turdus merula"}

	updated := conf.CloneSettings(current)
	updated.BirdNET.RangeFilter.Species = []string{"Corvus corax"}

	require.NotEmpty(t, restoreBlockedFields(current, updated))
	require.Equal(t, current.BirdNET.RangeFilter.Species, updated.BirdNET.RangeFilter.Species)

	updated.BirdNET.RangeFilter.Species[0] = "mutated"
	assert.Equal(t, "Turdus merula", current.BirdNET.RangeFilter.Species[0],
		"restored slice must not share a backing array with the previous snapshot")
}

// TestBlockedFieldMapNamesRealFields pins that every name in getBlockedFieldMap
// still resolves to a field on conf.Settings. The walk skips names it cannot
// resolve so a stale entry cannot panic a live request, which means a rename
// would otherwise silently un-block a field with no test failing.
func TestBlockedFieldMapNamesRealFields(t *testing.T) {
	t.Parallel()

	var walk func(structType reflect.Type, blocked map[string]any, prefix string)
	walk = func(structType reflect.Type, blocked map[string]any, prefix string) {
		for name, rule := range blocked {
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}

			field, ok := structType.FieldByName(name)
			if !assert.True(t, ok, "getBlockedFieldMap names %s, which is not a field of %s", path, structType) {
				continue
			}

			subfields, nested := rule.(map[string]any)
			if !nested {
				assert.Equal(t, true, rule, "%s must map to true or to a submap", path)
				continue
			}

			fieldType := field.Type
			for fieldType.Kind() == reflect.Pointer {
				fieldType = fieldType.Elem()
			}
			if assert.Equal(t, reflect.Struct, fieldType.Kind(),
				"%s carries nested restrictions but is not a struct", path) {
				walk(fieldType, subfields, path)
			}
		}
	}

	walk(reflect.TypeFor[conf.Settings](), getBlockedFieldMap(), "")
}

// blockedLeafPaths flattens getBlockedFieldMap to the dotted paths of its true
// leaves.
func blockedLeafPaths(blocked map[string]any, prefix string) []string {
	var paths []string
	for name, rule := range blocked {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		switch rule := rule.(type) {
		case bool:
			if rule {
				paths = append(paths, path)
			}
		case map[string]any:
			paths = append(paths, blockedLeafPaths(rule, path)...)
		}
	}
	return paths
}
