package openfauna

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Source describes one external-link provider from the registry: a display name,
// a sort order, an icon hint for the UI, and a URL template with {id}/{lang}
// placeholders (and, for the supplementary registry, {sci}/{sci_underscored}).
type Source struct {
	Name  string `json:"name"`
	Order int    `json:"order"`
	Icon  string `json:"icon"`
	URL   string `json:"url"`
}

// Link is one resolved external resource link, ready for the UI to render as a
// labeled card. Order is retained so callers can merge multiple registries and
// re-sort the combined set.
type Link struct {
	Name  string
	URL   string
	Icon  string
	Order int
}

var (
	sourcesOnce sync.Once
	sourcesReg  map[string]Source
)

// Sources returns the embedded OpenFauna sources registry, parsed once. A parse
// failure yields an empty registry (links simply do not render) and is logged.
func Sources() map[string]Source {
	sourcesOnce.Do(func() {
		var reg map[string]Source
		if err := json.Unmarshal(sourcesJSON, &reg); err != nil {
			GetLogger().Error("failed to parse embedded openfauna sources.json", logger.Error(err))
			reg = map[string]Source{}
		}
		sourcesReg = reg
	})
	return sourcesReg
}

// substituteTemplate fills {id}, {lang}, {sci}, {sci_underscored} placeholders in a
// URL template. Longer keys are replaced first so {sci_underscored} is not partially
// matched by {sci}. Callers pass only the vars relevant to their registry.
func substituteTemplate(tmpl string, vars map[string]string) string {
	out := tmpl
	for _, k := range []string{"sci_underscored", "sci", "lang", "id"} {
		if v, ok := vars[k]; ok {
			out = strings.ReplaceAll(out, "{"+k+"}", v)
		}
	}
	return out
}

// resolveLinks resolves a species' id-keyed links map against a registry into
// sorted Links. For each entry: a non-empty url override is used verbatim;
// otherwise {id}/{lang} are substituted into the source template. Entries whose
// source is absent from the registry, or that have neither an id nor an override,
// are skipped. Result is sorted by Source.Order, then Name for stability.
func resolveLinks(links map[string]LinkEntry, lang string, reg map[string]Source) []Link {
	out := make([]Link, 0, len(links))
	for id, entry := range links {
		src, ok := reg[id]
		if !ok {
			continue
		}
		var url string
		switch {
		case entry.URL != "":
			url = entry.URL
		case entry.ID != "":
			url = substituteTemplate(src.URL, map[string]string{"id": entry.ID, "lang": lang})
		default:
			continue
		}
		out = append(out, Link{Name: src.Name, URL: url, Icon: src.Icon, Order: src.Order})
	}
	sortLinks(out)
	return out
}

// sortLinks orders links by Order then Name (deterministic for equal orders).
func sortLinks(links []Link) {
	sort.SliceStable(links, func(i, j int) bool {
		if links[i].Order != links[j].Order {
			return links[i].Order < links[j].Order
		}
		return links[i].Name < links[j].Name
	})
}
