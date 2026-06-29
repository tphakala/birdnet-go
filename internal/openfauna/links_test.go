package openfauna

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// idWikipedia is the wikipedia source id, repeated across these table tests.
const idWikipedia = "wikipedia"

func TestResolveLinksTier1(t *testing.T) {
	reg := map[string]Source{
		idWikipedia:   {Name: "Wikipedia", Order: 10, Icon: idWikipedia, URL: "https://www.wikidata.org/wiki/Special:GoToLinkedPage/{lang}wiki/{id}"},
		"inaturalist": {Name: "iNaturalist", Order: 20, Icon: "inaturalist", URL: "https://www.inaturalist.org/taxa/{id}?locale={lang}"},
		"gbif":        {Name: "GBIF", Order: 30, Icon: "gbif", URL: "https://www.gbif.org/species/{id}"},
	}
	links := map[string]LinkEntry{
		"gbif":        {ID: "2480528"},
		idWikipedia:   {ID: "Q41181"},
		"inaturalist": {ID: "5074"},
	}
	got := resolveLinks(links, "de", reg)
	require.Len(t, got, 3)
	assert.Equal(t, "Wikipedia", got[0].Name)
	assert.Equal(t, idWikipedia, got[0].Icon)
	assert.Equal(t, "https://www.wikidata.org/wiki/Special:GoToLinkedPage/dewiki/Q41181", got[0].URL)
	assert.Equal(t, "https://www.inaturalist.org/taxa/5074?locale=de", got[1].URL)
	assert.Equal(t, "https://www.gbif.org/species/2480528", got[2].URL)
}

func TestResolveLinksHonorsURLOverride(t *testing.T) {
	reg := map[string]Source{
		idWikipedia: {Name: "Wikipedia", Order: 10, Icon: idWikipedia, URL: "https://www.wikidata.org/wiki/Special:GoToLinkedPage/{lang}wiki/{id}"},
	}
	links := map[string]LinkEntry{
		idWikipedia: {ID: "Q123", URL: "https://en.wikipedia.org/wiki/Common_blackbird"},
	}
	got := resolveLinks(links, "fi", reg)
	require.Len(t, got, 1)
	assert.Equal(t, "https://en.wikipedia.org/wiki/Common_blackbird", got[0].URL)
}

func TestResolveLinksSkipsUnknownSourceAndEmptyID(t *testing.T) {
	reg := map[string]Source{
		idWikipedia: {Name: "Wikipedia", Order: 10, Icon: idWikipedia, URL: "https://x/{id}"},
	}
	links := map[string]LinkEntry{
		idWikipedia: {ID: ""},   // no id, no override -> skip
		"mystery":   {ID: "42"}, // source not in registry -> skip
	}
	got := resolveLinks(links, "en", reg)
	assert.Empty(t, got)
}

func TestEmbeddedSourcesLoad(t *testing.T) {
	reg := Sources()
	for _, want := range []string{idWikipedia, "inaturalist", "gbif"} {
		_, ok := reg[want]
		assert.Truef(t, ok, "embedded sources.json missing %q", want)
	}
	assert.Equal(t, 10, reg[idWikipedia].Order, "wikipedia order")
}

func TestExternalLinksSupplementaryAppendsXenoCanto(t *testing.T) {
	// Aquila chrysaetos is in the embedded dataset with wikipedia+inaturalist ids.
	withSupp := ExternalLinks("Aquila chrysaetos", "en", true)
	icons := map[string]int{}
	for _, l := range withSupp {
		icons[l.Icon]++
	}
	assert.Positivef(t, icons["xeno-canto"], "supplementary on: expected a xeno-canto link, got %+v", withSupp)
	assert.Equalf(t, 1, icons[idWikipedia], "expected exactly one wikipedia link (no duplicate gap-fill), got %+v", withSupp)
}

func TestExternalLinksSupplementaryOffOmitsXenoCanto(t *testing.T) {
	off := ExternalLinks("Aquila chrysaetos", "en", false)
	for _, l := range off {
		assert.NotEqualf(t, "xeno-canto", l.Icon, "supplementary off: xeno-canto should not appear: %+v", off)
	}
}

func TestExternalLinksWikipediaGapFillForMissingSpecies(t *testing.T) {
	links := ExternalLinks("Madeupus nonexistus", "fr", true)
	var wiki, xc bool
	for _, l := range links {
		if l.Icon == idWikipedia {
			wiki = true
			assert.Equal(t, "https://fr.wikipedia.org/wiki/Madeupus_nonexistus", l.URL, "gap-fill wikipedia url")
		}
		if l.Icon == "xeno-canto" {
			xc = true
		}
	}
	assert.Truef(t, wiki, "missing-species gap-fill should include a wikipedia link: %+v", links)
	assert.Truef(t, xc, "missing-species gap-fill should include a xeno-canto link: %+v", links)
}
