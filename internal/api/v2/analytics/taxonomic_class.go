package analytics

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// chiropteraClass is the class name bat labels carry in the v2 schema. Bats are an order
// in the genus taxonomy, so a bat genus found there is reported under this name too.
const chiropteraClass = "Chiroptera"

// applyTaxonomicClasses sets each summary's TaxonomicClass to the class name of its v2
// label (for example "Aves" or "Chiroptera"), so the dashboard can group rows by taxon.
// Labels from multi-taxa models (Perch) carry no class; for those, and when the label
// lookup fails, the class comes from the bundled genus taxonomy (Aves, Mammalia,
// Insecta, ...), and a species it does not know keeps "". Without the v2 repositories
// (no v2 database) the summaries are left unchanged.
func (c *Handler) applyTaxonomicClasses(ctx context.Context, summaries []SpeciesDailySummary) {
	if c.labelRepo == nil || c.taxonomicClassRepo == nil || len(summaries) == 0 {
		return
	}

	labelClasses, err := c.labelClasses(ctx, summaries)
	if err != nil {
		c.LogWarnIfEnabled("Failed to read taxonomic classes from labels", logger.Error(err))
	}
	for i := range summaries {
		if class := labelClasses[summaries[i].ScientificName]; class != "" {
			summaries[i].TaxonomicClass = class
		} else if _, genus, ok := c.TaxonomyDB.LookupGenusByScientificName(summaries[i].ScientificName); ok {
			summaries[i].TaxonomicClass = genus.Class
			if genus.Order == chiropteraClass {
				summaries[i].TaxonomicClass = chiropteraClass
			}
		}
	}
}

// labelClasses maps each summary's scientific name to the class name of its v2 label. A
// scientific name has one label per model; if models disagree, the oldest label (lowest
// ID) with a known class wins, so the answer does not depend on query order.
func (c *Handler) labelClasses(ctx context.Context, summaries []SpeciesDailySummary) (map[string]string, error) {
	names := make([]string, len(summaries))
	for i := range summaries {
		names[i] = summaries[i].ScientificName
	}
	labelsByName, err := c.labelRepo.GetByScientificNames(ctx, names)
	if err != nil {
		return nil, err
	}
	classes, err := c.taxonomicClassRepo.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	classNameByID := make(map[uint]string, len(classes))
	for _, class := range classes {
		classNameByID[class.ID] = class.Name
	}

	classByName := make(map[string]string, len(labelsByName))
	for name, labels := range labelsByName {
		var chosenID uint
		for _, label := range labels {
			if label.TaxonomicClassID == nil || (chosenID != 0 && label.ID > chosenID) {
				continue
			}
			if class, ok := classNameByID[*label.TaxonomicClassID]; ok {
				classByName[name] = class
				chosenID = label.ID
			}
		}
	}
	return classByName, nil
}
