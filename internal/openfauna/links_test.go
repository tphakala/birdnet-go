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

// TestResolveLinksPerSourceLangMap verifies each source's lang_map is applied to
// {lang} for that source ONLY: the Wikipedia source remaps nb -> the "no" project,
// while iNaturalist (no lang_map) keeps the base "nb" subtag.
func TestResolveLinksPerSourceLangMap(t *testing.T) {
	t.Parallel()
	reg := map[string]Source{
		idWikipedia: {
			Name: "Wikipedia", Order: 10, Icon: idWikipedia,
			URL:     "https://www.wikidata.org/wiki/Special:GoToLinkedPage/{lang}wiki/{id}",
			LangMap: map[string]string{"nb": "no", "nn": "no"},
		},
		"inaturalist": {Name: "iNaturalist", Order: 20, Icon: "inaturalist", URL: "https://www.inaturalist.org/taxa/{id}?locale={lang}"},
	}
	links := map[string]LinkEntry{
		idWikipedia:   {ID: "Q41181"},
		"inaturalist": {ID: "5074"},
	}
	got := resolveLinks(links, "nb", reg)
	require.Len(t, got, 2)
	assert.Equal(t, "https://www.wikidata.org/wiki/Special:GoToLinkedPage/nowiki/Q41181", got[0].URL,
		"Wikipedia source remaps nb -> no")
	assert.Equal(t, "https://www.inaturalist.org/taxa/5074?locale=nb", got[1].URL,
		"iNaturalist keeps the base subtag; the Wikipedia mapping must not leak")
}

func TestSourceLangFor(t *testing.T) {
	t.Parallel()
	wiki := Source{LangMap: map[string]string{"nb": "no", "nn": "no"}}
	assert.Equal(t, "no", wiki.langFor("nb"))
	assert.Equal(t, "no", wiki.langFor("nn"))
	assert.Equal(t, "de", wiki.langFor("de"), "unmapped subtag falls through unchanged")
	// A source with no map returns the input verbatim.
	assert.Equal(t, "nb", Source{}.langFor("nb"))
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

// TestProductionRegistriesGetLangOverride verifies the birdnet-go-owned Wikipedia
// lang override is merged onto BOTH parsed registries (upstream + supplementary),
// so it survives a data refresh that regenerates data/sources.json.
func TestProductionRegistriesGetLangOverride(t *testing.T) {
	t.Parallel()
	if wiki, ok := Sources()["wikipedia"]; ok {
		assert.Equal(t, "no", wiki.langFor("nb"), "upstream Wikipedia source must map nb -> no")
	}
	if wiki, ok := supplementarySources()["wikipedia"]; ok {
		assert.Equal(t, "no", wiki.langFor("nb"), "supplementary Wikipedia source must map nb -> no")
	}
}

// TestApplySourceLangMapsMergesUpstream verifies the branch's core "survive data
// refreshes" premise: an upstream-supplied lang_map is MERGED with (not clobbered by)
// birdnet-go's own {nb,nn}->no override, so a future OpenFauna refresh that ships its
// own lang_map keys keeps them.
func TestApplySourceLangMapsMergesUpstream(t *testing.T) {
	reg := map[string]Source{
		idWikipedia: {
			Name:    "Wikipedia",
			URL:     "https://{lang}.wikipedia.org/wiki/{id}",
			LangMap: map[string]string{"als": "gsw", "nb": "upstream"}, // upstream-supplied
		},
		"inaturalist": {Name: "iNaturalist", URL: "https://www.inaturalist.org/taxa/{id}"},
	}
	applySourceLangMaps(reg)

	wiki := reg[idWikipedia]
	// Upstream-only key preserved.
	assert.Equal(t, "gsw", wiki.LangMap["als"], "upstream lang_map key must survive the merge")
	// birdnet-go override wins on the conflicting key and adds its own.
	assert.Equal(t, "no", wiki.LangMap["nb"], "birdnet-go override must win on a conflicting key")
	assert.Equal(t, "no", wiki.LangMap["nn"], "birdnet-go override key must be added")
	// A source with no birdnet-go override is untouched.
	assert.Nil(t, reg["inaturalist"].LangMap)
}
