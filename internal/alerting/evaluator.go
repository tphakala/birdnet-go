package alerting

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// EvaluateConditions checks if all conditions match against event properties.
// Returns true if ALL conditions are satisfied (AND logic).
// Empty conditions list returns true (no conditions = always match).
func EvaluateConditions(conditions []entities.AlertCondition, properties map[string]any) bool {
	for i := range conditions {
		if !evaluateCondition(&conditions[i], properties) {
			return false
		}
	}
	return true
}

func evaluateCondition(cond *entities.AlertCondition, properties map[string]any) bool {
	propVal, exists := properties[cond.Property]
	if !exists {
		return false
	}

	propStr := fmt.Sprintf("%v", propVal)
	condVal := cond.Value

	switch cond.Operator {
	case OperatorIs:
		return strings.EqualFold(propStr, condVal)
	case OperatorIsNot:
		return !strings.EqualFold(propStr, condVal)
	case OperatorContains:
		return strings.Contains(strings.ToLower(propStr), strings.ToLower(condVal))
	case OperatorNotContains:
		return !strings.Contains(strings.ToLower(propStr), strings.ToLower(condVal))
	case OperatorGreaterThan, OperatorLessThan, OperatorGreaterOrEqual, OperatorLessOrEqual:
		return evaluateNumeric(cond.Operator, propVal, condVal)
	default:
		return false
	}
}

func evaluateNumeric(operator string, propVal any, condVal string) bool {
	propFloat, err := toFloat64(propVal)
	if err != nil {
		return false
	}
	condFloat, err := strconv.ParseFloat(condVal, 64)
	if err != nil {
		return false
	}

	switch operator {
	case OperatorGreaterThan:
		return propFloat > condFloat
	case OperatorLessThan:
		return propFloat < condFloat
	case OperatorGreaterOrEqual:
		return propFloat >= condFloat
	case OperatorLessOrEqual:
		return propFloat <= condFloat
	default:
		return false
	}
}

func toFloat64(val any) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", val)
	}
}
