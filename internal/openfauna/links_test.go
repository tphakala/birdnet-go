package openfauna

import (
	"testing"
)

func TestResolveLinksTier1(t *testing.T) {
	reg := map[string]Source{
		"wikipedia":   {Name: "Wikipedia", Order: 10, Icon: "wikipedia", URL: "https://www.wikidata.org/wiki/Special:GoToLinkedPage/{lang}wiki/{id}"},
		"inaturalist": {Name: "iNaturalist", Order: 20, Icon: "inaturalist", URL: "https://www.inaturalist.org/taxa/{id}?locale={lang}"},
		"gbif":        {Name: "GBIF", Order: 30, Icon: "gbif", URL: "https://www.gbif.org/species/{id}"},
	}
	links := map[string]LinkEntry{
		"gbif":        {ID: "2480528"},
		"wikipedia":   {ID: "Q41181"},
		"inaturalist": {ID: "5074"},
	}
	got := resolveLinks(links, "de", reg)
	if len(got) != 3 {
		t.Fatalf("want 3 links, got %d: %+v", len(got), got)
	}
	if got[0].Name != "Wikipedia" || got[0].Icon != "wikipedia" ||
		got[0].URL != "https://www.wikidata.org/wiki/Special:GoToLinkedPage/dewiki/Q41181" {
		t.Fatalf("wikipedia link wrong: %+v", got[0])
	}
	if got[1].URL != "https://www.inaturalist.org/taxa/5074?locale=de" {
		t.Fatalf("inaturalist link wrong: %+v", got[1])
	}
	if got[2].URL != "https://www.gbif.org/species/2480528" {
		t.Fatalf("gbif link wrong: %+v", got[2])
	}
}

func TestResolveLinksHonorsURLOverride(t *testing.T) {
	reg := map[string]Source{
		"wikipedia": {Name: "Wikipedia", Order: 10, Icon: "wikipedia", URL: "https://www.wikidata.org/wiki/Special:GoToLinkedPage/{lang}wiki/{id}"},
	}
	links := map[string]LinkEntry{
		"wikipedia": {ID: "Q123", URL: "https://en.wikipedia.org/wiki/Common_blackbird"},
	}
	got := resolveLinks(links, "fi", reg)
	if len(got) != 1 || got[0].URL != "https://en.wikipedia.org/wiki/Common_blackbird" {
		t.Fatalf("url override not honored: %+v", got)
	}
}

func TestResolveLinksSkipsUnknownSourceAndEmptyID(t *testing.T) {
	reg := map[string]Source{
		"wikipedia": {Name: "Wikipedia", Order: 10, Icon: "wikipedia", URL: "https://x/{id}"},
	}
	links := map[string]LinkEntry{
		"wikipedia": {ID: ""},   // no id, no override -> skip
		"mystery":   {ID: "42"}, // source not in registry -> skip
	}
	got := resolveLinks(links, "en", reg)
	if len(got) != 0 {
		t.Fatalf("want 0 links, got %+v", got)
	}
}

func TestEmbeddedSourcesLoad(t *testing.T) {
	reg := Sources()
	for _, want := range []string{"wikipedia", "inaturalist", "gbif"} {
		if _, ok := reg[want]; !ok {
			t.Fatalf("embedded sources.json missing %q: %+v", want, reg)
		}
	}
	if reg["wikipedia"].Order != 10 {
		t.Fatalf("wikipedia order = %d, want 10", reg["wikipedia"].Order)
	}
}

func TestExternalLinksSupplementaryAppendsXenoCanto(t *testing.T) {
	// Aquila chrysaetos is in the embedded dataset with wikipedia+inaturalist ids.
	withSupp := ExternalLinks("Aquila chrysaetos", "en", true)
	icons := map[string]bool{}
	for _, l := range withSupp {
		icons[l.Icon] = true
	}
	if !icons["xeno-canto"] {
		t.Fatalf("supplementary on: expected a xeno-canto link, got %+v", withSupp)
	}
	wikiCount := 0
	for _, l := range withSupp {
		if l.Icon == "wikipedia" {
			wikiCount++
		}
	}
	if wikiCount != 1 {
		t.Fatalf("expected exactly one wikipedia link (no duplicate gap-fill), got %d: %+v", wikiCount, withSupp)
	}
}

func TestExternalLinksSupplementaryOffOmitsXenoCanto(t *testing.T) {
	off := ExternalLinks("Aquila chrysaetos", "en", false)
	for _, l := range off {
		if l.Icon == "xeno-canto" {
			t.Fatalf("supplementary off: xeno-canto should not appear: %+v", off)
		}
	}
}

func TestExternalLinksWikipediaGapFillForMissingSpecies(t *testing.T) {
	links := ExternalLinks("Madeupus nonexistus", "fr", true)
	var wiki, xc bool
	for _, l := range links {
		if l.Icon == "wikipedia" {
			wiki = true
			if l.URL != "https://fr.wikipedia.org/wiki/Madeupus_nonexistus" {
				t.Fatalf("gap-fill wikipedia url wrong: %s", l.URL)
			}
		}
		if l.Icon == "xeno-canto" {
			xc = true
		}
	}
	if !wiki || !xc {
		t.Fatalf("missing-species gap-fill incomplete: wiki=%v xc=%v (%+v)", wiki, xc, links)
	}
}
