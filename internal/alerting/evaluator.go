package alerting

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// SpeciesListResolver resolves a species list ID into a slice of species names.
type SpeciesListResolver interface {
	ResolveSpeciesList(id uint) []string
}

// EvaluateConditions checks if all conditions match against event properties.
// Returns true if ALL conditions are satisfied (AND logic).
// Empty conditions list returns true (no conditions = always match).
func EvaluateConditions(conditions []entities.AlertCondition, properties map[string]any, resolver SpeciesListResolver) bool {
	for i := range conditions {
		if !evaluateCondition(&conditions[i], properties, resolver) {
			return false
		}
	}
	return true
}

func evaluateCondition(cond *entities.AlertCondition, properties map[string]any, resolver SpeciesListResolver) bool {
	propVal, exists := properties[cond.Property]
	if !exists {
		return false
	}

	propStr := fmt.Sprintf("%v", propVal)
	condVal := cond.Value

	// Handle managed species list expansion for 'in'/'not_in' on species/scientific_name.
	// Members are stored as canonical lowercase scientific names (OpenFauna convention),
	// so we compare against the detection's scientific_name property directly.
	if resolver != nil &&
		(cond.Property == PropertyScientificName || cond.Property == PropertySpeciesName) &&
		(cond.Operator == OperatorIn || cond.Operator == OperatorNotIn) &&
		strings.HasPrefix(condVal, "list:") {

		listIDStr := strings.TrimPrefix(condVal, "list:")
		listID64, err := strconv.ParseUint(listIDStr, 10, 32)
		if err == nil {
			listID := uint(listID64)
			sciNames := resolver.ResolveSpeciesList(listID)
			
			// We compare list members (which are canonical scientific names) against
			// the event's scientific name.
			var searchName string
			if sciVal, ok := properties[PropertyScientificName]; ok {
				searchName = fmt.Sprintf("%v", sciVal)
			} else {
				searchName = propStr
			}
			
			propLower := strings.ToLower(searchName)
			found := false
			for _, sci := range sciNames {
				if sci == propLower {
					found = true
					break
				}
			}
			if cond.Operator == OperatorIn {
				return found
			}
			return !found
		}
	}

	switch cond.Operator {
	case OperatorIs:
		return strings.EqualFold(propStr, condVal)
	case OperatorIsNot:
		return !strings.EqualFold(propStr, condVal)
	case OperatorIn:
		return listContains(condVal, propStr)
	case OperatorNotIn:
		return !listContains(condVal, propStr)
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

// listContains checks whether propValue appears in a comma, semicolon, or newline
// delimited list. Items are trimmed, empty items are skipped, and comparisons are
// case-insensitive.
func listContains(listValue, propValue string) bool {
	if propValue == "" {
		return false
	}

	for item := range strings.FieldsFuncSeq(listValue, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if strings.EqualFold(trimmed, propValue) {
			return true
		}
	}
	return false
}

func evaluateNumeric(operator string, propVal any, condVal string) bool {
	propFloat, err := toFloat64(propVal)
	if err != nil {
		log := logger.Global().Module("alerting")
		log.Debug("Failed to parse property value for numeric evaluation",
			logger.String("operator", operator),
			logger.Error(err))
		return false
	}
	condFloat, err := strconv.ParseFloat(condVal, 64)
	if err != nil {
		log := logger.Global().Module("alerting")
		log.Warn("Alert condition has unparseable threshold value",
			logger.String("operator", operator),
			logger.String("condition_value", condVal),
			logger.Error(err))
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
