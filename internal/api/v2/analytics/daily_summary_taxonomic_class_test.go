package analytics

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakeLabelRepo serves GetByScientificNames from a map; other methods are unused.
type fakeLabelRepo struct {
	repository.LabelRepository
	labels map[string][]*entities.Label
	err    error
}

func (f *fakeLabelRepo) GetByScientificNames(_ context.Context, names []string) (map[string][]*entities.Label, error) {
	if f.err != nil {
		return nil, f.err
	}
	found := make(map[string][]*entities.Label, len(names))
	for _, name := range names {
		if labels, ok := f.labels[name]; ok {
			found[name] = labels
		}
	}
	return found, nil
}

// fakeTaxonomicClassRepo serves GetAll from a slice; other methods are unused.
type fakeTaxonomicClassRepo struct {
	repository.TaxonomicClassRepository
	classes []*entities.TaxonomicClass
}

func (f *fakeTaxonomicClassRepo) GetAll(_ context.Context) ([]*entities.TaxonomicClass, error) {
	return f.classes, nil
}

// TestApplyTaxonomicClasses pins the daily summary's taxonomic_class to the class of the
// species' v2 label, "" for a label without one, and no change when the lookup fails.
func TestApplyTaxonomicClasses(t *testing.T) {
	t.Parallel()

	const avesID, chiropteraID = 1, 2
	classes := &fakeTaxonomicClassRepo{classes: []*entities.TaxonomicClass{
		{ID: avesID, Name: "Aves"},
		{ID: chiropteraID, Name: "Chiroptera"},
	}}
	labels := map[string][]*entities.Label{
		"Turdus merula": {{TaxonomicClassID: new(uint(avesID))}},
		"Pipistrellus pipistrellus": {
			{ID: 30, TaxonomicClassID: new(uint(avesID))}, // a later, disagreeing label loses
			{ID: 20, TaxonomicClassID: nil},
			{ID: 10, TaxonomicClassID: new(uint(chiropteraID))},
		},
		"Vulpes vulpes":       {{TaxonomicClassID: nil}}, // Perch labels carry no class
		"Erithacus rubecula":  {{TaxonomicClassID: nil}},
		"Eptesicus serotinus": {{TaxonomicClassID: nil}},
	}
	summaries := func() []SpeciesDailySummary {
		return []SpeciesDailySummary{
			{ScientificName: "Turdus merula"},
			{ScientificName: "Pipistrellus pipistrellus"},
			{ScientificName: "Vulpes vulpes"},
			{ScientificName: "Erithacus rubecula"},
			{ScientificName: "Eptesicus serotinus"},
		}
	}
	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	require.NoError(t, err)

	t.Run("classes from labels", func(t *testing.T) {
		t.Parallel()
		core := &apicore.Core{TaxonomyDB: taxonomyDB}
		h := &Handler{Core: core, labelRepo: &fakeLabelRepo{labels: labels}, taxonomicClassRepo: classes}
		got := summaries()
		h.applyTaxonomicClasses(t.Context(), got)
		assert.Equal(t, "Aves", got[0].TaxonomicClass)
		assert.Equal(t, "Chiroptera", got[1].TaxonomicClass, "the oldest label with a class wins")
		assert.Equal(t, "Mammalia", got[2].TaxonomicClass, "a classless label takes its genus class")
		assert.Equal(t, "Aves", got[3].TaxonomicClass, "a classless label takes its genus class")
		assert.Equal(t, "Chiroptera", got[4].TaxonomicClass, "a bat genus reports the bat class")
	})

	t.Run("lookup failure falls back to the genus taxonomy", func(t *testing.T) {
		t.Parallel()
		repo := &fakeLabelRepo{err: errors.NewStd("db down")}
		h := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}, labelRepo: repo, taxonomicClassRepo: classes}
		got := summaries()
		h.applyTaxonomicClasses(t.Context(), got)
		assert.Equal(t, "Aves", got[0].TaxonomicClass)
		assert.Empty(t, got[1].TaxonomicClass, "a bat genus outside the taxonomy stays unclassified")
		assert.Equal(t, "Mammalia", got[2].TaxonomicClass)
	})

	t.Run("unknown species without a class stays empty", func(t *testing.T) {
		t.Parallel()
		repo := &fakeLabelRepo{labels: map[string][]*entities.Label{"Engine": {{}}}}
		h := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}, labelRepo: repo, taxonomicClassRepo: classes}
		got := []SpeciesDailySummary{{ScientificName: "Engine"}}
		h.applyTaxonomicClasses(t.Context(), got)
		assert.Empty(t, got[0].TaxonomicClass)
	})

	t.Run("no v2 repositories", func(t *testing.T) {
		t.Parallel()
		got := summaries()
		(&Handler{Core: &apicore.Core{}}).applyTaxonomicClasses(t.Context(), got)
		assert.Empty(t, got[0].TaxonomicClass)
	})
}

// TestSpeciesDailySummary_TaxonomicClassAlwaysSerialized pins taxonomic_class as always
// present: the dashboard reads "" as "no class" and a missing key as "not known yet".
func TestSpeciesDailySummary_TaxonomicClassAlwaysSerialized(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(SpeciesDailySummary{})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"taxonomic_class":""`)
}
