package alerting

import (
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRules(t *testing.T) {
	rules := DefaultRules()
	require.NotEmpty(t, rules, "should have default rules")

	for _, rule := range rules {
		assert.NotEmpty(t, rule.Name, "rule must have a name")
		assert.True(t, rule.Enabled, "default rules should be enabled")
		assert.True(t, rule.BuiltIn, "default rules should be marked built-in")
		assert.NotEmpty(t, rule.ObjectType, "rule must have object type")
		assert.NotEmpty(t, rule.TriggerType, "rule must have trigger type")
		assert.NotEmpty(t, rule.Actions, "rule must have at least one action")
		assert.Positive(t, rule.CooldownSec, "rule must have cooldown")

		if rule.TriggerType == TriggerTypeEvent {
			assert.NotEmpty(t, rule.EventName, "event rule must have event name: %s", rule.Name)
		}
		if rule.TriggerType == TriggerTypeMetric {
			assert.NotEmpty(t, rule.MetricName, "metric rule must have metric name: %s", rule.Name)
			assert.NotEmpty(t, rule.Conditions, "metric rule must have conditions: %s", rule.Name)
		}
	}
}

func TestDefaultRules_UniqueNames(t *testing.T) {
	rules := DefaultRules()
	names := make(map[string]bool, len(rules))
	for _, rule := range rules {
		assert.False(t, names[rule.Name], "duplicate rule name: %s", rule.Name)
		names[rule.Name] = true
	}
}

func TestDefaultRules_MetricStepsStartAtThreshold(t *testing.T) {
	// A rule with escalation steps fires only for a value at or above its lowest
	// step, so a step above the condition threshold would drop the samples in
	// between. The ladder re-arms only when a sample falls below the lowest step,
	// so a step below the threshold would keep it armed through a dip under the
	// threshold.
	var checked []string
	for _, rule := range DefaultRules() {
		if rule.TriggerType != TriggerTypeMetric || len(rule.EscalationSteps) == 0 {
			continue
		}
		checked = append(checked, rule.NameKey)

		require.Len(t, rule.Conditions, 1, "%s: one threshold condition", rule.Name)
		threshold, err := strconv.ParseFloat(rule.Conditions[0].Value, 64)
		require.NoError(t, err, "%s: condition value is a number", rule.Name)
		assert.InDelta(t, threshold, slices.Min(rule.EscalationSteps), 0, "%s: lowest step equals the condition threshold", rule.Name)
	}
	assert.Subset(t, checked, []string{RuleKeyHighCPUName, RuleKeyHighMemoryName, RuleKeyLowDiskName},
		"the metric rules that ship with steps are all checked")
}
