package alerting

import (
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
