package api

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Coverage of getBlockedFieldMap is split across several tests here because the
// leaves are not all reachable the same way:
//
//   - TestPatchCannotChangeBlockedFields drives real PATCH requests over every
//     blocked leaf a client can put in a request body. The set is not
//     hand-maintained: clientReachableBlockedLeaves derives it by reflection and
//     the test asserts the table matches it exactly, so a new leaf or a renamed
//     json tag fails here rather than silently going untested.
//   - TestPutCannotChangeBlockedFields does the same for the PUT path, which
//     enforces the map by a different mechanism.
//   - TestRestoreBlockedFieldsCoversEveryLeaf drives restoreBlockedFields
//     directly over ALL leaves, including the ones no request can reach, and is
//     what fails if a leaf is dropped from the map or from the walk.
//   - TestRestoreBlockedFieldsHandlesPointerSubtrees drives the pointer arms of
//     the walk, which no blocked path reaches today.
//   - TestBlockedFieldMapNamesRealFields pins that every name in the map still
//     resolves to a struct field, so a rename cannot silently un-block a field.
//   - TestPatchYamlDashFieldsAreEitherBlockedOrRederived pins the fields that
//     PUT protects via yaml:"-" but PATCH does not.
//
// Leaves tagged json:"-" (BirdNET.Labels, Realtime.Audio.SoxAudioTypes, Input)
// are covered by the direct test only. The JSON merge cannot reach them at all,
// so an HTTP-level immutability assertion would pass with enforcement deleted;
// listing them in both places would make coverage look better than it is.
//
// None of the HTTP-driving tests may call t.Parallel(): the PATCH path reaches
// restart.MarkRestartRequired, telemetry.UpdateTelemetryEnabled,
// imageprovider.SetCustomSynonyms and events.Emit, all process-global. The
// direct-call tests are pure and are parallel.

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

// clientReachableBlockedLeaves returns the blocked leaves a PATCH body can
// actually write: those nested under a settings section (no PATCH section maps
// to the root struct, so a top-level leaf is unreachable) whose every path
// component is visible to encoding/json.
//
// Deriving this rather than listing it is the point. The stored-value assertions
// in the HTTP table are only meaningful while the merge can reach the field, and
// that depends on json tags, which TestBlockedFieldMapNamesRealFields does not
// check because it resolves Go field names.
func clientReachableBlockedLeaves(t *testing.T) []string {
	t.Helper()

	jsonVisible := func(rt reflect.Type, name string) bool {
		f, ok := rt.FieldByName(name)
		require.True(t, ok, "%s is not a field of %s", name, rt)
		return f.Tag.Get("json") != "-"
	}

	var reachable []string
	for _, path := range blockedLeafPaths(getBlockedFieldMap(), "") {
		parts := strings.Split(path, ".")
		if len(parts) < 2 {
			continue // top-level: no section resolves to the root struct
		}
		rt := reflect.TypeFor[conf.Settings]()
		visible := true
		for _, part := range parts {
			if !jsonVisible(rt, part) {
				visible = false
				break
			}
			ft, _ := rt.FieldByName(part)
			next := ft.Type
			for next.Kind() == reflect.Pointer {
				next = next.Elem()
			}
			if next.Kind() == reflect.Struct {
				rt = next
			}
		}
		if visible {
			reachable = append(reachable, path)
		}
	}
	slices.Sort(reachable)
	return reachable
}

// TestPatchCannotChangeBlockedFields is the regression test for the enforcement
// gap: handleGenericSection merged the request straight into the section struct
// and then only appended a note to skippedFields saying restrictions existed, so
// every field getBlockedFieldMap marks never-settable was settable via PATCH.
//
// Every case asserts the path appears in skippedFields, which only
// restoreBlockedFields can populate. That is what makes each case non-vacuous
// independently of whether the stored value happens to be corrected downstream
// by something else, and it is why the two audio tool paths can live in this
// table at all (see their cases).
func TestPatchCannotChangeBlockedFields(t *testing.T) {
	seededTime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		leaf    string
		name    string
		section string
		seed    func(*conf.Settings)
		body    map[string]any
		// verify is optional: the audio tool paths deliberately assert nothing
		// about the stored value.
		verify func(*testing.T, *conf.Settings)
	}{
		{
			leaf:    "Security.SessionSecret",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.SessionSecret = "server-generated-secret" },
			body:    map[string]any{"sessionSecret": "attacker-pinned-secret"},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "server-generated-secret", s.Security.SessionSecret)
			},
		},
		{
			leaf:    "Security.SessionDuration",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.SessionDuration = 168 * time.Hour },
			body:    map[string]any{"sessionDuration": int64(100 * 365 * 24 * time.Hour)},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, 168*time.Hour, s.Security.SessionDuration)
			},
		},
		{
			leaf:    "Security.BasicAuth.ClientID",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.ClientID = "oauth-client-id" },
			body:    map[string]any{"basicAuth": map[string]any{"clientId": "attacker-client-id"}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "oauth-client-id", s.Security.BasicAuth.ClientID)
			},
		},
		{
			leaf:    "Security.BasicAuth.ClientSecret",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.ClientSecret = "oauth-client-secret" },
			body:    map[string]any{"basicAuth": map[string]any{"clientSecret": "attacker-client-secret"}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "oauth-client-secret", s.Security.BasicAuth.ClientSecret)
			},
		},
		{
			leaf:    "Security.BasicAuth.AuthCodeExp",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.AuthCodeExp = 10 * time.Minute },
			body:    map[string]any{"basicAuth": map[string]any{"authCodeExp": int64(365 * 24 * time.Hour)}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, 10*time.Minute, s.Security.BasicAuth.AuthCodeExp)
			},
		},
		{
			leaf:    "Security.BasicAuth.AccessTokenExp",
			section: "security",
			seed:    func(s *conf.Settings) { s.Security.BasicAuth.AccessTokenExp = time.Hour },
			body:    map[string]any{"basicAuth": map[string]any{"accessTokenExp": int64(365 * 24 * time.Hour)}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, time.Hour, s.Security.BasicAuth.AccessTokenExp)
			},
		},
		{
			leaf:    "Diagnostics.Profiling.Token",
			section: "diagnostics",
			seed:    func(s *conf.Settings) { s.Diagnostics.Profiling.Token = "server-minted-token" },
			body:    map[string]any{"profiling": map[string]any{"token": "attacker-known-token"}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "server-minted-token", s.Diagnostics.Profiling.Token)
			},
		},
		{
			leaf:    "BirdNET.RangeFilter.Model",
			section: "birdnet",
			seed:    func(s *conf.Settings) { s.BirdNET.RangeFilter.Model = "latest" },
			body:    map[string]any{"rangeFilter": map[string]any{"model": "legacy"}},
			verify: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "latest", s.BirdNET.RangeFilter.Model)
			},
		},
		{
			leaf:    "BirdNET.RangeFilter.Species",
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
			leaf:    "BirdNET.RangeFilter.LastUpdated",
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
		{
			// The audio tool paths assert only the rejection report, never the
			// stored value. conf.validateAudioSettings runs after the merge and
			// re-resolves both through ValidateToolPath, so a nonexistent attacker
			// path is discarded even with enforcement removed (the stored-value
			// assertion would pass for the wrong reason), while a path that does
			// exist makes the expectation depend on whether the machine running
			// the test has ffmpeg installed and where. skippedFields is computed
			// before conf.ValidateSettings and nothing else writes it.
			leaf:    "Realtime.Audio.FfmpegPath",
			name:    "Realtime.Audio.FfmpegPath via the audio section",
			section: "audio",
			seed:    func(s *conf.Settings) { s.Realtime.Audio.FfmpegPath = "/usr/bin/ffmpeg" },
			body:    map[string]any{"ffmpegPath": "/tmp/attacker/ffmpeg"},
		},
		{
			leaf:    "Realtime.Audio.SoxPath",
			name:    "Realtime.Audio.SoxPath via the audio section",
			section: "audio",
			seed:    func(s *conf.Settings) { s.Realtime.Audio.SoxPath = "/usr/bin/sox" },
			body:    map[string]any{"soxPath": "/tmp/attacker/sox"},
		},
	}

	// The table must cover exactly the client-reachable leaves, no more and no
	// fewer. Without this a new blocked leaf, or a json tag rename that makes an
	// existing one unreachable and its stored-value assertion vacuous, passes
	// unnoticed.
	covered := make([]string, 0, len(tests))
	for _, tt := range tests {
		if !slices.Contains(covered, tt.leaf) {
			covered = append(covered, tt.leaf)
		}
	}
	slices.Sort(covered)
	assert.Equal(t, clientReachableBlockedLeaves(t), covered,
		"the PATCH table must cover exactly the blocked leaves a request body can reach")

	for _, tt := range tests {
		name := tt.name
		if name == "" {
			name = tt.leaf
		}
		t.Run(name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)
			tt.seed(controller.Settings.Load())

			rec := patchSection(t, e, controller, tt.section, tt.body)

			assert.Contains(t, skippedFieldsOf(t, rec), tt.leaf,
				"a rejected blocked field must be reported, and only restoreBlockedFields can report it")
			if tt.verify != nil {
				tt.verify(t, controller.Settings.Load())
			}
		})
	}
}

// TestPatchCannotChangeBlockedTimestampLocation is the regression test for the
// bypass that made the restore unconditional, and it is what pins that the
// restore IS unconditional.
//
// blockedValuesEqual compares timestamps by instant, so this request compares
// equal and is deliberately NOT reported in skippedFields. It is still fully
// overwritten from the snapshot, Location included. Make the restore conditional
// on that comparison again and the attacker's Location survives, moving the
// calendar day conf.LocalNoon derives from it, which gates the daily
// range-filter rebuild and is reported to the UI by GET /api/v2/range/status.
//
// This is also the TZ-independent guard on that comparison: the +14:00 offset
// below differs from every local zone the suite might run in, so this fails in
// any TZ. The sibling assertion in TestRestoreBlockedFieldsIsQuietWhenNothingChanged
// only catches the offset-0 case.
func TestPatchCannotChangeBlockedTimestampLocation(t *testing.T) {
	seeded := time.Date(2026, 7, 25, 11, 0, 0, 0, time.UTC)

	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Load().BirdNET.RangeFilter.LastUpdated = seeded

	// The same instant, expressed in +14:00, which lands on the next calendar day.
	rec := patchSection(t, e, controller, "birdnet", map[string]any{
		"rangeFilter": map[string]any{"lastUpdated": "2026-07-26T01:00:00+14:00"},
	})

	stored := controller.Settings.Load().BirdNET.RangeFilter.LastUpdated
	assert.Equal(t, seeded.Day(), stored.Day(),
		"the attacker's Location must be overwritten, not just the instant")
	assert.Equal(t, seeded.Location().String(), stored.Location().String(),
		"the whole value is restored from the snapshot, Location included")
	assert.Empty(t, skippedFieldsOf(t, rec),
		"the same instant is not a reported change; reporting it would fire on every "+
			"ordinary birdnet save on a UTC server, where the JSON round trip rewrites Location")
}

// TestPatchReportsEmptyArrayNotNull pins the wire shape the endpoint
// documentation promises. skippedFieldsOf decodes into []string, where null and
// [] are both nil, so no other assertion in this file can tell them apart.
func TestPatchReportsEmptyArrayNotNull(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)

	rec := patchSection(t, e, controller, "security", map[string]any{"host": "birds.example.com"})

	assert.Contains(t, rec.Body.String(), `"skippedFields":[]`,
		"a request that rejected nothing must report [] rather than null")
}

// TestPutCannotChangeBlockedFields pins the PUT half of the contract: PUT now
// enforces the blocked-field map through restoreBlockedFields, the same function
// PATCH uses, so this drives the real UpdateSettings handler to confirm a blocked
// field survives a full-object PUT that tries to change it.
func TestPutCannotChangeBlockedFields(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)

	current := controller.Settings.Load()
	current.Security.SessionSecret = "server-generated-secret"
	current.Security.BasicAuth.ClientSecret = "oauth-client-secret"
	current.Diagnostics.Profiling.Token = "server-minted-token"
	current.BirdNET.RangeFilter.Model = "latest"

	tampered := conf.CloneSettings(current)
	tampered.Security.SessionSecret = "attacker-pinned-secret"
	tampered.Security.BasicAuth.ClientSecret = "attacker-client-secret"
	tampered.Diagnostics.Profiling.Token = "attacker-known-token"
	tampered.BirdNET.RangeFilter.Model = "legacy"
	tampered.Main.Name = "renamed node"

	putFullSettings(t, e, controller, tampered)

	updated := controller.Settings.Load()
	assert.Equal(t, "server-generated-secret", updated.Security.SessionSecret)
	assert.Equal(t, "oauth-client-secret", updated.Security.BasicAuth.ClientSecret)
	assert.Equal(t, "server-minted-token", updated.Diagnostics.Profiling.Token)
	assert.Equal(t, "latest", updated.BirdNET.RangeFilter.Model)
	assert.Equal(t, "renamed node", updated.Main.Name,
		"blocking the protected fields must not block the rest of the request")
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
//
// The redacted round trip is the case that pins the restoreRedactedSecrets ->
// restoreBlockedFields ordering. Swapping the two calls leaves the rest of the
// package green while making every ordinary security save report a phantom
// rejection of Security.SessionSecret and log a warning for it.
func TestPatchReportsBlockedFieldsItRejected(t *testing.T) {
	const realSecret = "server-generated-secret"

	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Load().Security.SessionSecret = realSecret

	rejected := patchSection(t, e, controller, "security",
		map[string]any{"sessionSecret": "attacker-pinned-secret"})
	assert.Equal(t, []string{"Security.SessionSecret"}, skippedFieldsOf(t, rejected),
		"a rejected field must be named in the response")

	clean := patchSection(t, e, controller, "security",
		map[string]any{"host": "birds.example.com"})
	assert.Empty(t, skippedFieldsOf(t, clean),
		"a request that touches nothing blocked must report nothing skipped")

	roundTrip := patchSection(t, e, controller, "security", map[string]any{
		"host":          "birds.example.com",
		"sessionSecret": redactedValue,
	})
	assert.Empty(t, skippedFieldsOf(t, roundTrip),
		"echoing the redacted placeholder back is not an attempt to change the secret")
	assert.Equal(t, realSecret, controller.Settings.Load().Security.SessionSecret,
		"the real secret must survive a redacted round trip")
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
// implementation that REPORTED every leaf unconditionally would still satisfy
// every immutability assertion in this file while logging a blocked-field
// warning on every settings write; this and the middle case of
// TestPatchReportsBlockedFieldsItRejected are what reject it.
//
// The two normalization cases are the ones that actually bite. Without the
// time.Time arm in blockedValuesEqual, a PATCH of the birdnet section on a
// running instance reports a phantom rejection; without the empty-slice arm, so
// does any instance whose range filter admits zero species.
func TestRestoreBlockedFieldsIsQuietWhenNothingChanged(t *testing.T) {
	t.Parallel()

	current := &conf.Settings{}
	current.Security.SessionSecret = "server-generated-secret"
	current.BirdNET.Labels = []string{"Turdus merula_Eurasian Blackbird"}
	// A monotonic-clock reading, as conf.UpdateIncludedSpecies leaves behind.
	current.BirdNET.RangeFilter.LastUpdated = time.Now()
	// Empty but non-nil, as a range filter admitting zero species leaves behind.
	current.BirdNET.RangeFilter.Species = []string{}

	updated := conf.CloneSettings(current)
	updated.Main.Name = "renamed node"
	// Model the merge's JSON round trip exactly rather than approximating it:
	// Round(0) alone drops the monotonic reading but leaves Location untouched,
	// whereas a real marshal/unmarshal also rewrites Location (to time.UTC at
	// offset 0). Approximating it here is what let a Location-sensitive
	// comparison look correct while phantom-rejecting on every UTC server.
	roundTripped, err := json.Marshal(current.BirdNET.RangeFilter.LastUpdated)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(roundTripped, &updated.BirdNET.RangeFilter.LastUpdated))
	// An empty slice comes back from the merge as nil.
	updated.BirdNET.RangeFilter.Species = nil

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

// pointerSubtreeLeaf and pointerSubtreeHolder are synthetic types for
// TestRestoreBlockedFieldsHandlesPointerSubtrees. conf.Settings has no blocked
// path behind a pointer, so the pointer arms of restoreBlockedSubtree cannot be
// reached through restoreBlockedFields at all.
type pointerSubtreeLeaf struct {
	Token string
	List  []string
	Other string
}

type pointerSubtreeHolder struct {
	Sub  *pointerSubtreeLeaf
	Tags map[string]string
}

// TestRestoreBlockedFieldsHandlesPointerSubtrees drives the arms that exist so
// that turning a blocked-path struct into a pointer later cannot silently
// disable enforcement underneath it. Without this the first execution of that
// code would be in production, on the day someone makes that change.
func TestRestoreBlockedFieldsHandlesPointerSubtrees(t *testing.T) {
	t.Parallel()

	blocked := map[string]any{
		"Sub":  map[string]any{"Token": true, "List": true},
		"Tags": true,
	}

	restore := func(current, updated *pointerSubtreeHolder) []string {
		var restored []string
		restoreBlockedFieldsRecursively(
			reflect.ValueOf(current).Elem(),
			reflect.ValueOf(updated).Elem(),
			blocked, &restored, "",
		)
		return restored
	}

	t.Run("both non-nil reverts the blocked leaf and keeps its sibling", func(t *testing.T) {
		t.Parallel()
		current := &pointerSubtreeHolder{Sub: &pointerSubtreeLeaf{Token: "real", Other: "before"}}
		updated := &pointerSubtreeHolder{Sub: &pointerSubtreeLeaf{Token: "attacker", Other: "after"}}

		assert.Equal(t, []string{"Sub.Token"}, restore(current, updated))
		assert.Equal(t, "real", updated.Sub.Token)
		assert.Equal(t, "after", updated.Sub.Other, "the unblocked sibling must survive")
	})

	t.Run("client nulled the struct, so the blocked leaf is rebuilt", func(t *testing.T) {
		t.Parallel()
		current := &pointerSubtreeHolder{Sub: &pointerSubtreeLeaf{Token: "real", Other: "before"}}
		updated := &pointerSubtreeHolder{}

		assert.Equal(t, []string{"Sub.Token"}, restore(current, updated))
		require.NotNil(t, updated.Sub, "a nulled struct holding a blocked leaf must be rebuilt")
		assert.Equal(t, "real", updated.Sub.Token)
		assert.Empty(t, updated.Sub.Other, "only the blocked leaf is rebuilt")
	})

	t.Run("nothing to restore leaves a nulled struct nil", func(t *testing.T) {
		t.Parallel()
		current := &pointerSubtreeHolder{Sub: &pointerSubtreeLeaf{Other: "before"}}
		updated := &pointerSubtreeHolder{}

		assert.Empty(t, restore(current, updated))
		assert.Nil(t, updated.Sub, "a no-op restore must not resurrect a pointer the client nulled")
	})

	t.Run("a nulled struct is rebuilt even when nothing is REPORTED", func(t *testing.T) {
		t.Parallel()
		// An empty non-nil slice compares equal to the rebuild's nil (they are the
		// same JSON), so this restores without reporting. The rebuild must still be
		// published: gating the write on the report count instead of on whether the
		// rebuild carries anything would let a client delete a struct holding
		// blocked fields simply by nulling it.
		current := &pointerSubtreeHolder{Sub: &pointerSubtreeLeaf{List: []string{}}}
		updated := &pointerSubtreeHolder{}

		assert.Empty(t, restore(current, updated), "an empty-vs-nil slice is not a reported change")
		require.NotNil(t, updated.Sub, "the blocked leaf must survive the client nulling its parent")
		assert.NotNil(t, updated.Sub.List, "and must keep its empty-but-present value")
	})

	t.Run("nil current zeroes the blocked leaf", func(t *testing.T) {
		t.Parallel()
		current := &pointerSubtreeHolder{}
		updated := &pointerSubtreeHolder{Sub: &pointerSubtreeLeaf{Token: "attacker", Other: "after"}}

		assert.Equal(t, []string{"Sub.Token"}, restore(current, updated))
		assert.Empty(t, updated.Sub.Token, "a nil current means the blocked leaf is the zero value")
		assert.Equal(t, "after", updated.Sub.Other)
	})

	t.Run("a restored map is copied, not aliased", func(t *testing.T) {
		t.Parallel()
		current := &pointerSubtreeHolder{Tags: map[string]string{"k": "real"}}
		updated := &pointerSubtreeHolder{Tags: map[string]string{"k": "attacker"}}

		assert.Equal(t, []string{"Tags"}, restore(current, updated))
		require.Equal(t, "real", updated.Tags["k"])

		updated.Tags["k"] = "mutated"
		assert.Equal(t, "real", current.Tags["k"],
			"restored map must not share storage with the previous snapshot")
	})
}

// TestPatchYamlDashFieldsAreEitherBlockedOrRederived pins the one place the two
// write paths differ. PUT skips every yaml:"-" field in addition to the blocked
// map; PATCH enforces the map alone, so a yaml:"-" field with a live json tag is
// merged from the request like any other.
//
// That is safe today only because everything in the gap is re-derived after the
// merge: conf.validateAudioSettings overwrites the three ffmpeg metadata fields
// from the actual binary (or clears them when it is missing). This test fails
// when a new yaml:"-" field becomes client-writable, forcing a decision between
// adding it to getBlockedFieldMap and confirming something re-derives it.
func TestPatchYamlDashFieldsAreEitherBlockedOrRederived(t *testing.T) {
	t.Parallel()

	// Fields that are yaml:"-", still visible to JSON, and NOT in the blocked
	// map. Each is re-derived by conf.validateAudioSettings after the merge.
	rederived := []string{
		"Realtime.Audio.FfmpegVersion",
		"Realtime.Audio.FfmpegMajor",
		"Realtime.Audio.FfmpegMinor",
	}

	blockedLeaves := blockedLeafPaths(getBlockedFieldMap(), "")

	var gap []string
	var walk func(rt reflect.Type, prefix string, depth int)
	walk = func(rt reflect.Type, prefix string, depth int) {
		if depth > 8 || rt.Kind() != reflect.Struct {
			return
		}
		for f := range rt.Fields() {
			if f.PkgPath != "" {
				continue
			}
			path := f.Name
			if prefix != "" {
				path = prefix + "." + f.Name
			}
			if f.Tag.Get("yaml") == "-" && f.Tag.Get("json") != "-" && !slices.Contains(blockedLeaves, path) {
				gap = append(gap, path)
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && ft != reflect.TypeFor[time.Time]() {
				walk(ft, path, depth+1)
			}
		}
	}
	walk(reflect.TypeFor[conf.Settings](), "", 0)

	slices.Sort(gap)
	slices.Sort(rederived)
	assert.Equal(t, rederived, gap,
		"a yaml:\"-\" field that JSON can still write is protected on PUT but not on PATCH; "+
			"add it to getBlockedFieldMap, or confirm it is re-derived after the merge and list it here")
}

// TestPatchFfmpegMetadataIsRederivedNotClientSettable exercises the other half of
// the claim above. The list in that test is only a list; this drives a real PATCH
// and asserts the re-derivation actually happens for two of the three fields.
//
// Asserts what the value is NOT, deliberately: validateAudioSettings sets these
// from the real binary when ffmpeg is present and clears them when it is absent,
// so any assertion on the resulting value would depend on the test machine.
//
// Two limits worth knowing before trusting it. It only distinguishes re-derivation
// from enforcement when ffmpeg is ON PATH: without it, validateAudioSettings takes
// its failure arm and clearFfmpegMetadata zeroes the same fields for an unrelated
// reason, so the assertions hold even if the re-derivation were deleted. CI
// installs ffmpeg on all three platforms, so that gap is a slim dev container
// only. And it asserts the value rather than the wiring, so renaming the json tag
// would make the PATCH stop reaching the field and the test pass vacuously.
func TestPatchFfmpegMetadataIsRederivedNotClientSettable(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	settings := controller.Settings.Load()
	settings.Realtime.Audio.FfmpegVersion = "6.0-real"
	settings.Realtime.Audio.FfmpegMajor = 6

	patchSection(t, e, controller, "audio", map[string]any{
		"ffmpegVersion": "99.9-attacker",
		"ffmpegMajor":   99,
	})

	updated := controller.Settings.Load()
	assert.NotEqual(t, "99.9-attacker", updated.Realtime.Audio.FfmpegVersion,
		"ffmpeg metadata must be re-derived from the binary, never taken from the request")
	assert.NotEqual(t, 99, updated.Realtime.Audio.FfmpegMajor,
		"ffmpeg metadata must be re-derived from the binary, never taken from the request")
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
