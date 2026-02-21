package entities

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAlertRuleJSONKeys verifies that AlertRule serializes with snake_case
// keys matching the frontend TypeScript interface. Without explicit json tags
// Go defaults to PascalCase, which causes the frontend's {#each rule (rule.id)}
// to receive undefined keys and trigger Svelte's each_key_duplicate error.
func TestAlertRuleJSONKeys(t *testing.T) {
	t.Parallel()

	rule := AlertRule{
		ID:          42,
		Name:        "test rule",
		Description: "desc",
		Enabled:     true,
		BuiltIn:     false,
		ObjectType:  "system",
		TriggerType: "metric",
		EventName:   "",
		MetricName:  "system.cpu_usage",
		CooldownSec: 300,
		CreatedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Conditions: []AlertCondition{
			{ID: 1, RuleID: 42, Property: "value", Operator: ">", Value: "90", DurationSec: 300, SortOrder: 0},
		},
		Actions: []AlertAction{
			{ID: 1, RuleID: 42, Target: "bell", TemplateTitle: "CPU Alert", TemplateMessage: "High CPU", SortOrder: 0},
		},
	}

	data, err := json.Marshal(rule)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	// Verify all top-level keys are snake_case matching the TypeScript AlertRule interface
	expectedKeys := []string{
		"id", "name", "description", "enabled", "built_in",
		"object_type", "trigger_type", "event_name", "metric_name",
		"cooldown_sec", "created_at", "updated_at", "conditions", "actions",
	}
	for _, key := range expectedKeys {
		assert.Contains(t, m, key, "JSON should contain snake_case key %q", key)
	}

	// Verify no PascalCase keys leaked through (the bug we're fixing)
	pascalKeys := []string{
		"ID", "Name", "Description", "Enabled", "BuiltIn",
		"ObjectType", "TriggerType", "EventName", "MetricName",
		"CooldownSec", "CreatedAt", "UpdatedAt", "Conditions", "Actions",
	}
	for _, key := range pascalKeys {
		assert.NotContains(t, m, key, "JSON should NOT contain PascalCase key %q", key)
	}

	// Verify the id value is correct (the critical field for {#each} keying)
	assert.EqualValues(t, 42, m["id"], "id should be 42")
}

// TestAlertConditionJSONKeys verifies AlertCondition serializes with snake_case keys.
func TestAlertConditionJSONKeys(t *testing.T) {
	t.Parallel()

	cond := AlertCondition{
		ID:          1,
		RuleID:      42,
		Property:    "value",
		Operator:    ">",
		Value:       "90",
		DurationSec: 300,
		SortOrder:   0,
	}

	data, err := json.Marshal(cond)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	expectedKeys := []string{"id", "rule_id", "property", "operator", "value", "duration_sec", "sort_order"}
	for _, key := range expectedKeys {
		assert.Contains(t, m, key, "JSON should contain snake_case key %q", key)
	}

	assert.NotContains(t, m, "RuleID", "JSON should NOT contain PascalCase key RuleID")
	assert.NotContains(t, m, "DurationSec", "JSON should NOT contain PascalCase key DurationSec")
}

// TestAlertActionJSONKeys verifies AlertAction serializes with snake_case keys.
func TestAlertActionJSONKeys(t *testing.T) {
	t.Parallel()

	action := AlertAction{
		ID:              1,
		RuleID:          42,
		Target:          "bell",
		TemplateTitle:   "title",
		TemplateMessage: "msg",
		SortOrder:       0,
	}

	data, err := json.Marshal(action)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	expectedKeys := []string{"id", "rule_id", "target", "template_title", "template_message", "sort_order"}
	for _, key := range expectedKeys {
		assert.Contains(t, m, key, "JSON should contain snake_case key %q", key)
	}

	assert.NotContains(t, m, "RuleID", "JSON should NOT contain PascalCase key RuleID")
	assert.NotContains(t, m, "TemplateTitle", "JSON should NOT contain PascalCase key TemplateTitle")
}

// TestAlertHistoryJSONKeys verifies AlertHistory serializes with snake_case keys.
func TestAlertHistoryJSONKeys(t *testing.T) {
	t.Parallel()

	hist := AlertHistory{
		ID:        1,
		RuleID:    42,
		FiredAt:   time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		EventData: `{"key":"value"}`,
		Actions:   "bell",
		CreatedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(hist)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	expectedKeys := []string{"id", "rule_id", "fired_at", "event_data", "actions", "created_at"}
	for _, key := range expectedKeys {
		assert.Contains(t, m, key, "JSON should contain snake_case key %q", key)
	}

	assert.NotContains(t, m, "RuleID", "JSON should NOT contain PascalCase key RuleID")
	assert.NotContains(t, m, "FiredAt", "JSON should NOT contain PascalCase key FiredAt")

	// Rule field should be omitted when zero-value (omitzero)
	assert.NotContains(t, m, "rule", "Zero-value Rule should be omitted with omitzero tag")
}

// TestAlertRuleJSONRoundTrip verifies that AlertRule can be serialized and
// deserialized without data loss, ensuring the json tags are bidirectionally correct.
func TestAlertRuleJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := AlertRule{
		ID:          7,
		Name:        "High CPU usage",
		Description: "Notifies when CPU usage exceeds 90%",
		Enabled:     true,
		BuiltIn:     true,
		ObjectType:  "system",
		TriggerType: "metric",
		EventName:   "",
		MetricName:  "system.cpu_usage",
		CooldownSec: 900,
		CreatedAt:   time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC),
		Conditions: []AlertCondition{
			{ID: 10, RuleID: 7, Property: "value", Operator: ">", Value: "90", DurationSec: 300, SortOrder: 0},
		},
		Actions: []AlertAction{
			{ID: 20, RuleID: 7, Target: "bell", TemplateTitle: "CPU Alert", TemplateMessage: "CPU is high", SortOrder: 0},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded AlertRule
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.Name, decoded.Name)
	assert.Equal(t, original.Description, decoded.Description)
	assert.Equal(t, original.Enabled, decoded.Enabled)
	assert.Equal(t, original.BuiltIn, decoded.BuiltIn)
	assert.Equal(t, original.ObjectType, decoded.ObjectType)
	assert.Equal(t, original.TriggerType, decoded.TriggerType)
	assert.Equal(t, original.EventName, decoded.EventName)
	assert.Equal(t, original.MetricName, decoded.MetricName)
	assert.Equal(t, original.CooldownSec, decoded.CooldownSec)
	assert.True(t, original.CreatedAt.Equal(decoded.CreatedAt))
	assert.True(t, original.UpdatedAt.Equal(decoded.UpdatedAt))

	require.Len(t, decoded.Conditions, 1)
	assert.Equal(t, original.Conditions[0].Property, decoded.Conditions[0].Property)
	assert.Equal(t, original.Conditions[0].Operator, decoded.Conditions[0].Operator)
	assert.Equal(t, original.Conditions[0].Value, decoded.Conditions[0].Value)

	require.Len(t, decoded.Actions, 1)
	assert.Equal(t, original.Actions[0].Target, decoded.Actions[0].Target)
	assert.Equal(t, original.Actions[0].TemplateTitle, decoded.Actions[0].TemplateTitle)
}

// TestAlertRuleJSONEmptyCollections verifies that rules with no conditions
// or actions serialize correctly (empty arrays, not null).
func TestAlertRuleJSONEmptyCollections(t *testing.T) {
	t.Parallel()

	rule := AlertRule{
		ID:          1,
		Name:        "simple rule",
		ObjectType:  "stream",
		TriggerType: "event",
		EventName:   "stream.disconnected",
		Conditions:  []AlertCondition{},
		Actions:     []AlertAction{},
	}

	data, err := json.Marshal(rule)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	// Empty slices should serialize as [] not null
	conditions, ok := m["conditions"].([]any)
	require.True(t, ok, "conditions should be an array")
	assert.Empty(t, conditions)

	actions, ok := m["actions"].([]any)
	require.True(t, ok, "actions should be an array")
	assert.Empty(t, actions)
}

// TestAlertRuleJSONNilCollections verifies that nil conditions/actions
// serialize as null (Go default for nil slices).
func TestAlertRuleJSONNilCollections(t *testing.T) {
	t.Parallel()

	rule := AlertRule{
		ID:          1,
		Name:        "rule with nil slices",
		ObjectType:  "system",
		TriggerType: "metric",
	}

	data, err := json.Marshal(rule)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	// Nil slices serialize as null in JSON
	assert.Nil(t, m["conditions"], "nil Conditions should serialize as null")
	assert.Nil(t, m["actions"], "nil Actions should serialize as null")
}

// TestAlertRuleJSONFromSnakeCaseInput verifies that the frontend's snake_case
// JSON can be correctly deserialized into Go structs. This simulates the
// request body from createAlertRule/updateAlertRule API calls.
func TestAlertRuleJSONFromSnakeCaseInput(t *testing.T) {
	t.Parallel()

	input := `{
		"name": "Test Rule",
		"description": "Created from frontend",
		"enabled": true,
		"built_in": false,
		"object_type": "system",
		"trigger_type": "metric",
		"metric_name": "system.cpu_usage",
		"cooldown_sec": 600,
		"conditions": [
			{"property": "value", "operator": ">", "value": "80", "duration_sec": 120}
		],
		"actions": [
			{"target": "bell", "template_title": "Alert", "template_message": "CPU high"}
		]
	}`

	var rule AlertRule
	require.NoError(t, json.Unmarshal([]byte(input), &rule))

	assert.Equal(t, "Test Rule", rule.Name)
	assert.Equal(t, "Created from frontend", rule.Description)
	assert.True(t, rule.Enabled)
	assert.False(t, rule.BuiltIn)
	assert.Equal(t, "system", rule.ObjectType)
	assert.Equal(t, "metric", rule.TriggerType)
	assert.Equal(t, "system.cpu_usage", rule.MetricName)
	assert.Equal(t, 600, rule.CooldownSec)

	require.Len(t, rule.Conditions, 1)
	assert.Equal(t, "value", rule.Conditions[0].Property)
	assert.Equal(t, ">", rule.Conditions[0].Operator)
	assert.Equal(t, "80", rule.Conditions[0].Value)
	assert.Equal(t, 120, rule.Conditions[0].DurationSec)

	require.Len(t, rule.Actions, 1)
	assert.Equal(t, "bell", rule.Actions[0].Target)
	assert.Equal(t, "Alert", rule.Actions[0].TemplateTitle)
}

// TestAlertRuleListJSONUniqueIDs verifies that a list of rules serialized as
// JSON produces unique "id" values — the direct contract the frontend's
// {#each filteredRules as rule (rule.id)} depends on.
func TestAlertRuleListJSONUniqueIDs(t *testing.T) {
	t.Parallel()

	rules := []AlertRule{
		{ID: 1, Name: "Rule A", ObjectType: "stream", TriggerType: "event"},
		{ID: 2, Name: "Rule B", ObjectType: "system", TriggerType: "metric"},
		{ID: 3, Name: "Rule C", ObjectType: "device", TriggerType: "event"},
	}

	data, err := json.Marshal(rules)
	require.NoError(t, err)

	var parsed []map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	require.Len(t, parsed, 3)

	ids := make(map[float64]bool)
	for _, r := range parsed {
		id, ok := r["id"].(float64)
		require.True(t, ok, "id should be a number, got %T", r["id"])
		assert.False(t, ids[id], "duplicate id %v found", id)
		ids[id] = true
	}
}

// TestAlertHistoryJSONWithRule verifies that AlertHistory with a populated
// Rule association serializes correctly including the nested rule.
func TestAlertHistoryJSONWithRule(t *testing.T) {
	t.Parallel()

	hist := AlertHistory{
		ID:      1,
		RuleID:  42,
		FiredAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Rule: AlertRule{
			ID:          42,
			Name:        "High CPU",
			ObjectType:  "system",
			TriggerType: "metric",
		},
	}

	data, err := json.Marshal(hist)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	// When Rule is populated, it should be present in JSON
	ruleData, ok := m["rule"].(map[string]any)
	require.True(t, ok, "rule should be a nested object when populated")
	assert.EqualValues(t, 42, ruleData["id"])
	assert.Equal(t, "High CPU", ruleData["name"])
}
