package alerts

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository/mocks"
)

const (
	updateTestRuleID = 1

	// Values the tests change on the stored High CPU usage rule.
	loweredThreshold = "80"
	longerSustainSec = 600
	customStepLow    = 95.0
	customStepHigh   = 99.0
	renamedRule      = "My CPU alert"

	// secondConditionValue is the threshold of the extra condition in a
	// two-condition rule.
	secondConditionValue = "95"
)

// storedHighCPURule returns the High CPU usage default as it sits in the
// database after seeding: built-in, with its single threshold condition and its
// escalation step.
func storedHighCPURule(t *testing.T) *entities.AlertRule {
	t.Helper()
	defaults := alerting.DefaultRules()
	i := slices.IndexFunc(defaults, func(def entities.AlertRule) bool {
		return def.NameKey == alerting.RuleKeyHighCPUName
	})
	require.GreaterOrEqual(t, i, 0, "default rule %s exists", alerting.RuleKeyHighCPUName)

	rule := defaults[i]
	rule.ID = updateTestRuleID
	require.True(t, rule.BuiltIn, "precondition: the default is built-in")
	require.NotEmpty(t, rule.EscalationSteps, "precondition: the default carries escalation steps")
	require.Len(t, rule.Conditions, 1, "precondition: one threshold condition")
	return &rule
}

// storedTwoConditionHighCPURule returns the High CPU usage default with a second
// threshold condition, as a custom setup with two conditions would be stored.
func storedTwoConditionHighCPURule(t *testing.T) *entities.AlertRule {
	t.Helper()
	rule := storedHighCPURule(t)
	second := rule.Conditions[0]
	second.Value = secondConditionValue
	rule.Conditions = append(rule.Conditions, second)
	return rule
}

// storedLegacyHighCPURule returns the High CPU usage default as a rule seeded
// before name keys existed: built-in, with no name key.
func storedLegacyHighCPURule(t *testing.T) *entities.AlertRule {
	t.Helper()
	rule := storedHighCPURule(t)
	rule.NameKey = ""
	return rule
}

// editorBody returns the JSON the settings rule editor sends for rule: the rule
// as stored, without the fields the editor does not know about (built_in and
// escalation_steps) and without timestamps. extra fields are merged on top.
func editorBody(t *testing.T, rule *entities.AlertRule, extra map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(rule)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	for _, field := range []string{"built_in", "escalation_steps", "created_at", "updated_at"} {
		delete(body, field)
	}
	maps.Copy(body, extra)
	out, err := json.Marshal(body)
	require.NoError(t, err)
	return string(out)
}

// putAlertRule sends body to UpdateAlertRule with existing as the stored rule and
// returns the rule handed to the repository, which is also what the handler
// returns in the response.
func putAlertRule(t *testing.T, existing *entities.AlertRule, body string) *entities.AlertRule {
	t.Helper()
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	h := New(apitest.NewCore(t))

	var saved *entities.AlertRule
	repo := mocks.NewMockAlertRuleRepository(t)
	repo.EXPECT().GetRule(mock.Anything, uint(updateTestRuleID)).Return(existing, nil).Once()
	repo.EXPECT().UpdateRule(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, rule *entities.AlertRule) error {
			saved = rule
			return nil
		}).Once()
	h.alertRuleRepo = repo

	req := httptest.NewRequest(http.MethodPut, "/api/v2/alerts/rules/1", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("1")

	require.NoError(t, h.UpdateAlertRule(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, saved, "the rule reached the repository")
	return saved
}

func TestUpdateAlertRule_KeepsServerOwnedFields(t *testing.T) {
	sameSteps := func(stored *entities.AlertRule) []float64 { return stored.EscalationSteps }
	noSteps := func(*entities.AlertRule) []float64 { return nil }

	tests := []struct {
		name string
		// stored returns the stored rule; edit changes the copy the editor sends
		// back, and extra adds body fields the editor does not send.
		stored func(t *testing.T) *entities.AlertRule
		edit   func(rule *entities.AlertRule)
		extra  func(stored *entities.AlertRule) map[string]any
		// wantSteps returns the steps expected on the saved rule.
		wantBuiltIn  bool
		wantSteps    func(stored *entities.AlertRule) []float64
		whyItMatters string
	}{
		{
			name:         "editor save of an unchanged threshold keeps built-in and steps",
			stored:       storedHighCPURule,
			edit:         func(rule *entities.AlertRule) { rule.CooldownSec++ },
			wantBuiltIn:  true,
			wantSteps:    sameSteps,
			whyItMatters: "the editor sends neither field, and both used to be zeroed",
		},
		{
			name:         "a lowered threshold drops the steps and keeps built-in",
			stored:       storedHighCPURule,
			edit:         func(rule *entities.AlertRule) { rule.Conditions[0].Value = loweredThreshold },
			wantBuiltIn:  true,
			wantSteps:    noSteps,
			whyItMatters: "a step above the threshold would silence alerts between the two",
		},
		{
			name:         "a changed sustain time keeps the steps",
			stored:       storedHighCPURule,
			edit:         func(rule *entities.AlertRule) { rule.Conditions[0].DurationSec = longerSustainSec },
			wantBuiltIn:  true,
			wantSteps:    sameSteps,
			whyItMatters: "steps do not depend on how long a condition must hold",
		},
		{
			name:   "different steps in the body win over the stored ones",
			stored: storedHighCPURule,
			edit:   func(*entities.AlertRule) {},
			extra: func(*entities.AlertRule) map[string]any {
				return map[string]any{"escalation_steps": []float64{customStepLow, customStepHigh}}
			},
			wantBuiltIn:  true,
			wantSteps:    func(*entities.AlertRule) []float64 { return []float64{customStepLow, customStepHigh} },
			whyItMatters: "API clients can still set steps",
		},
		{
			name:   "steps read back unchanged do not survive a lowered threshold",
			stored: storedHighCPURule,
			edit:   func(rule *entities.AlertRule) { rule.Conditions[0].Value = loweredThreshold },
			extra: func(stored *entities.AlertRule) map[string]any {
				return map[string]any{"escalation_steps": stored.EscalationSteps}
			},
			wantBuiltIn:  true,
			wantSteps:    noSteps,
			whyItMatters: "a client that reads, edits and writes back must not keep steps above the new threshold",
		},
		{
			name:   "a renamed built-in rule becomes a custom rule",
			stored: storedHighCPURule,
			edit: func(rule *entities.AlertRule) {
				rule.Name = renamedRule
				rule.NameKey = ""
			},
			wantBuiltIn:  false,
			wantSteps:    sameSteps,
			whyItMatters: "a renamed built-in plus the default seeded on restart would be two built-ins that cannot be deleted",
		},
		{
			name:   "the same conditions in another order keep the steps",
			stored: storedTwoConditionHighCPURule,
			edit:   func(rule *entities.AlertRule) { slices.Reverse(rule.Conditions) },

			wantBuiltIn:  true,
			wantSteps:    sameSteps,
			whyItMatters: "conditions are ANDed, so their order is not a change of thresholds",
		},
		{
			name:         "clearing the name key alone is a rename",
			stored:       storedHighCPURule,
			edit:         func(rule *entities.AlertRule) { rule.NameKey = "" },
			wantBuiltIn:  false,
			wantSteps:    sameSteps,
			whyItMatters: "the key identifies the rule, so a request without it no longer names the default",
		},
		{
			name:         "a legacy built-in rule without a name key renamed becomes a custom rule",
			stored:       storedLegacyHighCPURule,
			edit:         func(rule *entities.AlertRule) { rule.Name = renamedRule },
			wantBuiltIn:  false,
			wantSteps:    sameSteps,
			whyItMatters: "without a key the name is the only identity, and startup seeds the default again",
		},
		{
			name:         "a legacy built-in rule without a name key saved unchanged stays built-in",
			stored:       storedLegacyHighCPURule,
			edit:         func(rule *entities.AlertRule) { rule.CooldownSec++ },
			wantBuiltIn:  true,
			wantSteps:    sameSteps,
			whyItMatters: "an unchanged name is not a rename, key or no key",
		},
		{
			name: "built_in in the body cannot promote a custom rule",
			stored: func(t *testing.T) *entities.AlertRule {
				t.Helper()
				rule := storedHighCPURule(t)
				rule.BuiltIn = false
				rule.NameKey = ""
				rule.EscalationSteps = nil
				return rule
			},
			edit: func(*entities.AlertRule) {},
			extra: func(*entities.AlertRule) map[string]any {
				return map[string]any{"built_in": true}
			},
			wantBuiltIn:  false,
			wantSteps:    noSteps,
			whyItMatters: "an update keeps the stored built_in flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := tt.stored(t)
			edited := tt.stored(t)
			tt.edit(edited)
			var extra map[string]any
			if tt.extra != nil {
				extra = tt.extra(stored)
			}

			saved := putAlertRule(t, stored, editorBody(t, edited, extra))

			assert.Equal(t, tt.wantBuiltIn, saved.BuiltIn, tt.whyItMatters)
			assert.Equal(t, tt.wantSteps(stored), saved.EscalationSteps, tt.whyItMatters)
		})
	}
}

func TestUpdateAlertRule_KeepsKeyedNameAndDescription(t *testing.T) {
	const (
		translatedName        = "Korkea suorittimen käyttö"
		translatedDescription = "Ilmoittaa, kun suorittimen käyttö ylittää rajan"
		editedDescription     = "My own description"
	)

	tests := []struct {
		name            string
		edit            func(rule *entities.AlertRule)
		extra           map[string]any
		renamed         bool
		wantDescription func(stored *entities.AlertRule) string
		wantSteps       func(stored *entities.AlertRule) []float64
	}{
		{
			name: "translated text with both keys kept leaves the stored text",
			edit: func(rule *entities.AlertRule) {
				rule.Name = translatedName
				rule.Description = translatedDescription
			},
			wantDescription: func(stored *entities.AlertRule) string { return stored.Description },
			wantSteps:       func(stored *entities.AlertRule) []float64 { return stored.EscalationSteps },
		},
		{
			name: "an edited description clears its key and is saved as typed",
			edit: func(rule *entities.AlertRule) {
				rule.Name = translatedName
				rule.Description = editedDescription
				rule.DescriptionKey = ""
			},
			wantDescription: func(*entities.AlertRule) string { return editedDescription },
			wantSteps:       func(stored *entities.AlertRule) []float64 { return stored.EscalationSteps },
		},
		{
			name: "translated text together with different steps still leaves the stored name",
			edit: func(rule *entities.AlertRule) {
				rule.Name = translatedName
				rule.Description = translatedDescription
			},
			extra:           map[string]any{"escalation_steps": []float64{customStepLow, customStepHigh}},
			wantDescription: func(stored *entities.AlertRule) string { return stored.Description },
			wantSteps:       func(*entities.AlertRule) []float64 { return []float64{customStepLow, customStepHigh} },
		},
		{
			name: "an edited name clears its key and is saved as typed",
			edit: func(rule *entities.AlertRule) {
				rule.Name = renamedRule
				rule.NameKey = ""
			},
			renamed:         true,
			wantDescription: func(stored *entities.AlertRule) string { return stored.Description },
			wantSteps:       func(stored *entities.AlertRule) []float64 { return stored.EscalationSteps },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := storedHighCPURule(t)
			require.NotEmpty(t, stored.NameKey, "precondition: the default has a name key")
			require.NotEmpty(t, stored.DescriptionKey, "precondition: the default has a description key")
			edited := storedHighCPURule(t)
			tt.edit(edited)

			saved := putAlertRule(t, stored, editorBody(t, edited, tt.extra))

			wantName := storedHighCPURule(t).Name
			if tt.renamed {
				wantName = renamedRule
			}
			assert.Equal(t, wantName, saved.Name, "with the key kept, the stored name stays the shipped English name; a cleared key means a typed name")
			assert.Equal(t, tt.wantDescription(storedHighCPURule(t)), saved.Description)
			assert.Equal(t, !tt.renamed, saved.BuiltIn, "a localized save is not a rename")
			assert.Equal(t, tt.wantSteps(storedHighCPURule(t)), saved.EscalationSteps)
		})
	}
}

func TestUpdateAlertRule_SavesTextOfARuleWithoutKeys(t *testing.T) {
	const editedDescription = "My own description"

	stored := storedHighCPURule(t)
	stored.BuiltIn = false
	stored.NameKey = ""
	stored.DescriptionKey = ""
	stored.EscalationSteps = nil
	edited := *stored
	edited.Name = renamedRule
	edited.Description = editedDescription

	saved := putAlertRule(t, stored, editorBody(t, &edited, nil))

	assert.Equal(t, renamedRule, saved.Name, "a rule with no name key has no stored text to protect")
	assert.Equal(t, editedDescription, saved.Description, "a rule with no description key has no stored text to protect")
	assert.False(t, saved.BuiltIn)
}
