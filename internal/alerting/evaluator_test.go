package alerting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestEvaluateConditions_EmptyConditions(t *testing.T) {
	result := EvaluateConditions(nil, map[string]any{"species_name": "Robin"})
	assert.True(t, result, "empty conditions should match")
}

func TestEvaluateConditions_StringOperators(t *testing.T) {
	tests := []struct {
		name     string
		operator string
		property string
		value    string
		propVal  any
		want     bool
	}{
		{"is match", OperatorIs, "species_name", "Robin", "Robin", true},
		{"is case insensitive", OperatorIs, "species_name", "robin", "Robin", true},
		{"is no match", OperatorIs, "species_name", "Eagle", "Robin", false},
		{"is_not match", OperatorIsNot, "species_name", "Eagle", "Robin", true},
		{"is_not no match", OperatorIsNot, "species_name", "Robin", "Robin", false},
		{"contains match", OperatorContains, "species_name", "Owl", "Great Horned Owl", true},
		{"contains case insensitive", OperatorContains, "species_name", "owl", "Great Horned Owl", true},
		{"contains no match", OperatorContains, "species_name", "Eagle", "Great Horned Owl", false},
		{"not_contains match", OperatorNotContains, "species_name", "Eagle", "Great Horned Owl", true},
		{"not_contains no match", OperatorNotContains, "species_name", "Owl", "Great Horned Owl", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conds := []entities.AlertCondition{
				{Property: tt.property, Operator: tt.operator, Value: tt.value},
			}
			props := map[string]any{tt.property: tt.propVal}
			assert.Equal(t, tt.want, EvaluateConditions(conds, props))
		})
	}
}

func TestEvaluateConditions_NumericOperators(t *testing.T) {
	tests := []struct {
		name     string
		operator string
		value    string
		propVal  any
		want     bool
	}{
		{"gt float64 true", OperatorGreaterThan, "0.90", 0.95, true},
		{"gt float64 false", OperatorGreaterThan, "0.90", 0.85, false},
		{"gt float64 equal", OperatorGreaterThan, "0.90", 0.90, false},
		{"lt true", OperatorLessThan, "50", 30.0, true},
		{"lt false", OperatorLessThan, "50", 60.0, false},
		{"gte true equal", OperatorGreaterOrEqual, "90", 90.0, true},
		{"gte true greater", OperatorGreaterOrEqual, "90", 91.0, true},
		{"gte false", OperatorGreaterOrEqual, "90", 89.0, false},
		{"lte true equal", OperatorLessOrEqual, "90", 90.0, true},
		{"lte false", OperatorLessOrEqual, "90", 91.0, false},
		{"int property", OperatorGreaterThan, "50", 60, true},
		{"int64 property", OperatorGreaterThan, "50", int64(60), true},
		{"string property coercion", OperatorGreaterThan, "0.85", "0.95", true},
		{"float32 property", OperatorGreaterThan, "0.50", float32(0.75), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conds := []entities.AlertCondition{
				{Property: PropertyConfidence, Operator: tt.operator, Value: tt.value},
			}
			props := map[string]any{PropertyConfidence: tt.propVal}
			assert.Equal(t, tt.want, EvaluateConditions(conds, props))
		})
	}
}

func TestEvaluateConditions_MissingProperty(t *testing.T) {
	conds := []entities.AlertCondition{
		{Property: "nonexistent", Operator: OperatorIs, Value: "test"},
	}
	result := EvaluateConditions(conds, map[string]any{"species_name": "Robin"})
	assert.False(t, result, "missing property should fail condition")
}

func TestEvaluateConditions_MultipleConditionsAND(t *testing.T) {
	conds := []entities.AlertCondition{
		{Property: PropertySpeciesName, Operator: OperatorContains, Value: "Owl"},
		{Property: PropertyConfidence, Operator: OperatorGreaterThan, Value: "0.90"},
	}

	t.Run("both match", func(t *testing.T) {
		props := map[string]any{PropertySpeciesName: "Great Horned Owl", PropertyConfidence: 0.95}
		assert.True(t, EvaluateConditions(conds, props))
	})

	t.Run("first fails", func(t *testing.T) {
		props := map[string]any{PropertySpeciesName: "Robin", PropertyConfidence: 0.95}
		assert.False(t, EvaluateConditions(conds, props))
	})

	t.Run("second fails", func(t *testing.T) {
		props := map[string]any{PropertySpeciesName: "Great Horned Owl", PropertyConfidence: 0.80}
		assert.False(t, EvaluateConditions(conds, props))
	})
}

func TestEvaluateConditions_UnknownOperator(t *testing.T) {
	conds := []entities.AlertCondition{
		{Property: "species_name", Operator: "unknown_op", Value: "test"},
	}
	result := EvaluateConditions(conds, map[string]any{"species_name": "Robin"})
	assert.False(t, result, "unknown operator should fail condition")
}

func TestEvaluateConditions_InvalidNumericValue(t *testing.T) {
	conds := []entities.AlertCondition{
		{Property: PropertyConfidence, Operator: OperatorGreaterThan, Value: "not_a_number"},
	}
	result := EvaluateConditions(conds, map[string]any{PropertyConfidence: 0.95})
	assert.False(t, result, "non-numeric condition value should fail")
}

func TestEvaluateConditions_UnsupportedPropertyType(t *testing.T) {
	conds := []entities.AlertCondition{
		{Property: PropertyConfidence, Operator: OperatorGreaterThan, Value: "0.50"},
	}

	// bool is not a supported numeric type
	result := EvaluateConditions(conds, map[string]any{PropertyConfidence: true})
	assert.False(t, result, "unsupported type (bool) should fail numeric comparison")

	// struct is not supported
	type custom struct{ val int }
	result = EvaluateConditions(conds, map[string]any{PropertyConfidence: custom{val: 1}})
	assert.False(t, result, "unsupported type (struct) should fail numeric comparison")
}

func TestEvaluateConditions_AdditionalIntTypes(t *testing.T) {
	conds := []entities.AlertCondition{
		{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "50"},
	}

	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"uint64", uint64(60), true},
		{"uint32", uint32(60), true},
		{"int32", int32(60), true},
		{"uint8", uint8(60), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := map[string]any{PropertyValue: tt.val}
			assert.Equal(t, tt.want, EvaluateConditions(conds, props))
		})
	}
}
