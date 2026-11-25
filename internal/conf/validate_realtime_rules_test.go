package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateModelConsensusRuleModels(t *testing.T) {
	t.Parallel()

	rules := func(models ...string) *ModelConsensusSettings {
		return &ModelConsensusSettings{Rules: []ModelConsensusRule{{Models: models}}}
	}
	tests := []struct {
		name    string
		current *ModelConsensusSettings
		updated *ModelConsensusSettings
		wantErr string
	}{
		{"known alias accepted", &ModelConsensusSettings{}, rules(ModelIDPerchV2), ""},
		{"uppercase known alias accepted", &ModelConsensusSettings{}, rules("PERCH_V2"), ""},
		{"catalog spelling accepted", &ModelConsensusSettings{}, rules(ModelIDPerchV2Catalog), ""},
		{"no models accepted", &ModelConsensusSettings{}, &ModelConsensusSettings{Rules: []ModelConsensusRule{{MinModels: 2}}}, ""},
		{"unknown alias rejected", &ModelConsensusSettings{}, rules("future_model"), "future_model"},
		{"unknown alias already stored is accepted in another case", rules("Future_Model"), rules("future_model"), ""},
		{"whitespace-only entry rejected", rules("  "), rules("  "), "rules[0]"},
		{"empty entry rejected", &ModelConsensusSettings{}, rules(""), "rules[0]"},
		{"error names the rule index", &ModelConsensusSettings{},
			&ModelConsensusSettings{Rules: []ModelConsensusRule{{MinModels: 2}, {Models: []string{"nope"}}}}, "rules[1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateModelConsensusRuleModels(tt.current, tt.updated)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
