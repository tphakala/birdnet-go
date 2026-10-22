package alerts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository/mocks"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// Names used by the duplicate tests.
	translatedLowDisk = "Wenig Speicherplatz"
	customRuleA       = "Custom A"
	customRuleB       = "Custom B"
	lowDiskDefault    = "Low disk space"

	// customCooldownSec is a cooldown that differs from every shipped default.
	customCooldownSec = 7777

	resetDefaultsPath = "/api/v2/alerts/rules/reset-defaults"
	importRulesPath   = "/api/v2/alerts/rules/import"
)

// defaultsTestResponse decodes the reset and import response bodies. Fields a
// response does not carry stay zero.
type defaultsTestResponse struct {
	Status   string    `json:"status"`
	Created  *int      `json:"created"`
	Imported *int      `json:"imported"`
	Total    *int      `json:"total"`
	Skipped  *[]string `json:"skipped"`
}

// newDefaultsTestHandler returns a handler backed by a real SQLite repository,
// since the behaviour under test is which rows end up stored. Foreign keys are
// on, so deleting a rule cascades to its conditions and actions.
func newDefaultsTestHandler(t *testing.T) (*Handler, repository.AlertRuleRepository) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "alerts.db") + "?_foreign_keys=ON"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(
		&entities.AlertRule{}, &entities.AlertCondition{}, &entities.AlertAction{}, &entities.AlertHistory{},
	))

	repo := repository.NewAlertRuleRepository(db, nil)
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	h := New(apitest.NewCore(t))
	h.alertRuleRepo = repo
	return h, repo
}

// seedRules stores copies of rules and returns the stored rows.
func seedRules(t *testing.T, repo repository.AlertRuleRepository, rules []entities.AlertRule) {
	t.Helper()
	for i := range rules {
		rule := rules[i]
		require.NoError(t, repo.CreateRule(t.Context(), &rule))
	}
}

// defaultsExcept returns the shipped defaults without the one with the given name key.
func defaultsExcept(nameKey string) []entities.AlertRule {
	var out []entities.AlertRule
	defaults := alerting.DefaultRules()
	for i := range defaults {
		if defaults[i].NameKey != nameKey {
			out = append(out, defaults[i])
		}
	}
	return out
}

// lowDiskSavedByEditor returns the Low disk space default as the settings
// editor saved it in a German UI: a translated name, not built-in, no steps.
func lowDiskSavedByEditor() entities.AlertRule {
	defaults := alerting.DefaultRules()
	for i := range defaults {
		if defaults[i].NameKey != alerting.RuleKeyLowDiskName {
			continue
		}
		rule := defaults[i]
		rule.Name = translatedLowDisk
		rule.BuiltIn = false
		rule.EscalationSteps = nil
		return rule
	}
	return entities.AlertRule{}
}

// customRule returns a keyless user rule with the given name.
func customRule(name string) entities.AlertRule {
	return entities.AlertRule{
		Name: name, Enabled: true, ObjectType: "stream", TriggerType: "event",
		EventName: "stream.error", CooldownSec: customCooldownSec,
		Conditions: []entities.AlertCondition{},
		Actions:    []entities.AlertAction{{Target: "bell"}},
	}
}

func storedRules(t *testing.T, repo repository.AlertRuleRepository) []entities.AlertRule {
	t.Helper()
	rules, err := repo.ListRules(t.Context(), repository.AlertRuleFilter{})
	require.NoError(t, err)
	return rules
}

// countBy counts stored rules per value of key.
func countBy(rules []entities.AlertRule, key func(*entities.AlertRule) string) map[string]int {
	counts := make(map[string]int)
	for i := range rules {
		if k := key(&rules[i]); k != "" {
			counts[k]++
		}
	}
	return counts
}

func nameOf(r *entities.AlertRule) string    { return r.Name }
func nameKeyOf(r *entities.AlertRule) string { return r.NameKey }

// postJSON runs handler with a POST request and decodes the response.
func postJSON(t *testing.T, handler func(echo.Context) error, path, body string) defaultsTestResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, handler(echo.New().NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp defaultsTestResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestResetDefaultAlertRules_DoesNotDuplicateDefaults(t *testing.T) {
	defaultCount := len(alerting.DefaultRules())

	customised := alerting.DefaultRules()
	for i := range customised {
		if customised[i].NameKey == alerting.RuleKeyLowDiskName {
			customised[i].CooldownSec = customCooldownSec
			customised[i].BuiltIn = true
		}
	}
	damaged := append(defaultsExcept(alerting.RuleKeyLowDiskName), lowDiskSavedByEditor())

	tests := []struct {
		name        string
		seed        []entities.AlertRule
		wantCreated int
		wantSkipped []string
		// wantKept is a stored name that must still be there once, and the
		// number of rules expected in total.
		wantKept  string
		wantTotal int
	}{
		{
			name:        "an unrelated custom rule is kept and every default is recreated",
			seed:        append(customised, customRule(customRuleA)),
			wantCreated: defaultCount, wantSkipped: []string{},
			wantKept: customRuleA, wantTotal: defaultCount + 1,
		},
		{
			name:        "a custom rule with a default's name blocks that default",
			seed:        append(alerting.DefaultRules(), customRule(lowDiskDefault)),
			wantCreated: defaultCount - 1, wantSkipped: []string{lowDiskDefault},
			wantKept: lowDiskDefault, wantTotal: defaultCount,
		},
		{
			name:        "a non-built-in rule with a default's name key blocks that default",
			seed:        damaged,
			wantCreated: defaultCount - 1, wantSkipped: []string{lowDiskDefault},
			wantKept: translatedLowDisk, wantTotal: defaultCount,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, repo := newDefaultsTestHandler(t)
			seedRules(t, repo, tt.seed)

			resp := postJSON(t, h.ResetDefaultAlertRules, resetDefaultsPath, "")
			assert.Equal(t, "defaults reset", resp.Status)
			require.NotNil(t, resp.Created)
			assert.Equal(t, tt.wantCreated, *resp.Created)
			require.NotNil(t, resp.Skipped, "skipped is [] and never null")
			assert.Equal(t, tt.wantSkipped, *resp.Skipped)

			rules := storedRules(t, repo)
			assert.Len(t, rules, tt.wantTotal)
			assert.Equal(t, 1, countBy(rules, nameOf)[tt.wantKept])
			for key, n := range countBy(rules, nameKeyOf) {
				assert.Equal(t, 1, n, "name key %s is stored once", key)
			}
			for i := range rules {
				if rules[i].NameKey == alerting.RuleKeyLowDiskName && rules[i].BuiltIn {
					assert.NotEqual(t, customCooldownSec, rules[i].CooldownSec, "a built-in rule has its shipped values again")
				}
			}

			// A second reset leaves the same set of rules.
			again := postJSON(t, h.ResetDefaultAlertRules, resetDefaultsPath, "")
			assert.Len(t, storedRules(t, repo), tt.wantTotal)
			require.NotNil(t, again.Skipped)
			assert.Equal(t, tt.wantSkipped, *again.Skipped)
		})
	}
}

func TestImportAlertRules_SkipsDuplicates(t *testing.T) {
	defaultCount := len(alerting.DefaultRules())

	lowDiskTranslated := lowDiskSavedByEditor()

	tests := []struct {
		name         string
		seed         []entities.AlertRule
		payload      []entities.AlertRule
		wantImported int
		wantSkipped  []string
		wantTotal    int
	}{
		{
			name:         "a rule with the name of a stored default is skipped",
			seed:         alerting.DefaultRules(),
			payload:      []entities.AlertRule{customRule(lowDiskDefault)},
			wantImported: 0, wantSkipped: []string{lowDiskDefault}, wantTotal: defaultCount,
		},
		{
			name:         "a rule with a stored default's name key under another name is skipped",
			seed:         alerting.DefaultRules(),
			payload:      []entities.AlertRule{lowDiskTranslated},
			wantImported: 0, wantSkipped: []string{translatedLowDisk}, wantTotal: defaultCount,
		},
		{
			name:         "a keyless rule with a new name is imported",
			seed:         []entities.AlertRule{customRule(customRuleA)},
			payload:      []entities.AlertRule{customRule(customRuleB)},
			wantImported: 1, wantSkipped: []string{}, wantTotal: 2,
		},
		{
			name:         "a name repeated in the payload is imported once",
			payload:      []entities.AlertRule{customRule(customRuleB), customRule(customRuleB)},
			wantImported: 1, wantSkipped: []string{customRuleB}, wantTotal: 1,
		},
		{
			name:         "a keyless rule with the name of a stored keyless rule is skipped",
			seed:         []entities.AlertRule{customRule(customRuleA)},
			payload:      []entities.AlertRule{customRule(customRuleA)},
			wantImported: 0, wantSkipped: []string{customRuleA}, wantTotal: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, repo := newDefaultsTestHandler(t)
			seedRules(t, repo, tt.seed)
			body, err := json.Marshal(map[string]any{"rules": tt.payload, "version": 1})
			require.NoError(t, err)

			resp := postJSON(t, h.ImportAlertRules, importRulesPath, string(body))
			require.NotNil(t, resp.Imported)
			require.NotNil(t, resp.Total)
			assert.Equal(t, tt.wantImported, *resp.Imported)
			assert.Equal(t, len(tt.payload), *resp.Total)
			require.NotNil(t, resp.Skipped, "skipped is [] and never null")
			assert.Equal(t, tt.wantSkipped, *resp.Skipped)

			rules := storedRules(t, repo)
			assert.Len(t, rules, tt.wantTotal)
			for key, n := range countBy(rules, nameOf) {
				assert.Equal(t, 1, n, "name %s is stored once", key)
			}
		})
	}
}

func TestImportAlertRules_OwnExportIsANoOp(t *testing.T) {
	h, repo := newDefaultsTestHandler(t)
	seedRules(t, repo, alerting.DefaultRules())
	seedRules(t, repo, []entities.AlertRule{customRule(customRuleA)})
	before := storedRules(t, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/rules/export", http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, h.ExportAlertRules(echo.New().NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)
	export := rec.Body.String()

	for range 2 {
		resp := postJSON(t, h.ImportAlertRules, importRulesPath, export)
		require.NotNil(t, resp.Imported)
		assert.Zero(t, *resp.Imported)
		require.NotNil(t, resp.Skipped)
		assert.Len(t, *resp.Skipped, len(before))
		assert.Len(t, storedRules(t, repo), len(before))
	}
}

// errListRules is the failure the repository mock returns in the failure tests.
var errListRules = errors.NewStd("list rules failed")

// newMockedDefaultsHandler returns a handler whose repository is a mock.
func newMockedDefaultsHandler(t *testing.T) (*Handler, *mocks.MockAlertRuleRepository) {
	t.Helper()
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	h := New(apitest.NewCore(t))
	repo := mocks.NewMockAlertRuleRepository(t)
	h.alertRuleRepo = repo
	return h, repo
}

func TestResetDefaultAlertRules_ListFailureDeletesNothing(t *testing.T) {
	h, repo := newMockedDefaultsHandler(t)
	repo.EXPECT().ListRules(mock.Anything, mock.Anything).Return(nil, errListRules).Once()
	// No DeleteBuiltInRules expectation: the mock fails the test if it is called.

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, resetDefaultsPath, http.NoBody)
	require.NoError(t, h.ResetDefaultAlertRules(echo.New().NewContext(req, rec)))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestResetDefaultAlertRules_CreateFailureIsNotCounted(t *testing.T) {
	h, repo := newMockedDefaultsHandler(t)
	repo.EXPECT().ListRules(mock.Anything, mock.Anything).Return(nil, nil).Once()
	repo.EXPECT().DeleteBuiltInRules(mock.Anything).Return(int64(0), nil).Once()
	repo.EXPECT().CreateRule(mock.Anything, mock.Anything).Return(errListRules)

	resp := postJSON(t, h.ResetDefaultAlertRules, resetDefaultsPath, "")
	require.NotNil(t, resp.Created)
	assert.Zero(t, *resp.Created)
	require.NotNil(t, resp.Skipped)
	assert.Empty(t, *resp.Skipped)
}

func TestImportAlertRules_ListFailureImportsNothing(t *testing.T) {
	h, repo := newMockedDefaultsHandler(t)
	repo.EXPECT().ListRules(mock.Anything, mock.Anything).Return(nil, errListRules).Once()
	// No CreateRule expectation: the mock fails the test if it is called.

	body := `{"rules":[{"name":"Custom A","object_type":"stream","trigger_type":"event"}],"version":1}`
	req := httptest.NewRequest(http.MethodPost, importRulesPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	require.NoError(t, h.ImportAlertRules(echo.New().NewContext(req, rec)))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
