package alerting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestRuleNameIndex_Has(t *testing.T) {
	const (
		storedName = "Stored rule"
		storedKey  = "settings.alerts.stored.name"
	)
	index := NewRuleNameIndex([]entities.AlertRule{
		{Name: storedName, NameKey: storedKey},
		{Name: "Keyless rule"},
	})

	tests := []struct {
		name string
		rule entities.AlertRule
		want bool
	}{
		{"same name", entities.AlertRule{Name: storedName}, true},
		{"same key under another name", entities.AlertRule{Name: "Translated", NameKey: storedKey}, true},
		{"unknown name and key", entities.AlertRule{Name: "Other", NameKey: "settings.alerts.other.name"}, false},
		{"keyless rule with an unknown name", entities.AlertRule{Name: "Other"}, false},
		{"an empty key never matches the empty key of a stored rule", entities.AlertRule{Name: "Other", NameKey: ""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, index.Has(&tt.rule))
		})
	}

	added := entities.AlertRule{Name: "Added", NameKey: "settings.alerts.added.name"}
	assert.False(t, index.Has(&added))
	index.Add(&added)
	assert.True(t, index.Has(&added))
}
