package main

import (
	"bytes"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"
)

// buildDateLayout is the layout of a build record's date (an ISO 8601 calendar
// date); the gallery formats it for display.
const buildDateLayout = "2006-01-02"

// manifestBuild mirrors one record of a manifest's "builds" map: the release
// notes of one build of one release. The maintainer-facing "changes",
// "built_at" and "upstream_commit" fields are not read.
type manifestBuild struct {
	// Build is a pointer so a missing value is told apart from zero.
	Build           *int     `json:"build"`
	UpstreamVersion string   `json:"upstream_version"`
	Date            string   `json:"date"`
	Reason          string   `json:"reason"`
	Notes           []string `json:"notes"`
}

// buildRecord is one validated release-notes record, ready to render as a
// CatalogBuild literal.
type buildRecord struct {
	version string
	build   int
	date    string
	reason  string
	notes   []string
}

// buildsFuncName is the name of the generated function that returns the
// family's build records: its variants function with "RegionalVariants"
// replaced by "Builds" (perchV2RegionalVariants -> perchV2Builds).
func (f *family) buildsFuncName() string {
	return strings.TrimSuffix(f.funcName, "RegionalVariants") + "Builds"
}

// buildRecords returns the release-notes records of the family's current
// manifest, sorted by release and build. A record with neither a reason nor
// notes is skipped: it carries only the maintainer-facing changes text, which
// the gallery never shows. Every other record must name a build of 1 or more of
// a release the family hosts (its listed releases, or for a one-release family a
// release one of its regional entries carries), a calendar date and no empty
// note, and a (release, build) pair may appear once.
func (f *family) buildRecords(m *manifest) ([]buildRecord, error) {
	releases := f.releases
	if len(releases) == 0 {
		regional := regionalEntries(m)
		for i := range regional {
			if v := regional[i].UpstreamVersion; !slices.Contains(releases, v) {
				releases = append(releases, v)
			}
		}
	}

	type key struct {
		version string
		build   int
	}
	seen := make(map[key]string, len(m.Builds))
	var out []buildRecord
	// Walk the tags in sorted order so the error for a duplicate is deterministic.
	for _, tag := range slices.Sorted(maps.Keys(m.Builds)) {
		b := m.Builds[tag]
		if b.Reason == "" && len(b.Notes) == 0 {
			continue
		}
		if b.Build == nil || *b.Build < 1 {
			return nil, fmt.Errorf("build record %s: missing build or build below 1", tag)
		}
		if !slices.Contains(releases, b.UpstreamVersion) {
			return nil, fmt.Errorf("build record %s: release %q is not one of %v", tag, b.UpstreamVersion, releases)
		}
		if _, err := time.Parse(buildDateLayout, b.Date); err != nil {
			return nil, fmt.Errorf("build record %s: date %q is not a YYYY-MM-DD date", tag, b.Date)
		}
		for _, n := range b.Notes {
			if strings.TrimSpace(n) == "" {
				return nil, fmt.Errorf("build record %s: empty note", tag)
			}
		}
		k := key{b.UpstreamVersion, *b.Build}
		if prev, dup := seen[k]; dup {
			return nil, fmt.Errorf("build record %s: duplicate release %s build %d (also %s)", tag, k.version, k.build, prev)
		}
		seen[k] = tag
		out = append(out, buildRecord{version: b.UpstreamVersion, build: *b.Build, date: b.Date, reason: b.Reason, notes: b.Notes})
	}
	slices.SortFunc(out, func(a, b buildRecord) int {
		if c := strings.Compare(a.version, b.version); c != 0 {
			return c
		}
		return a.build - b.build
	})
	return out, nil
}

// writeBuilds appends the family's builds function to buf: the release notes of
// its current manifest m as CatalogBuild literals, or nil when it has none.
func (f *family) writeBuilds(buf *bytes.Buffer, m *manifest) error {
	records, err := f.buildRecords(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(buf, "\n// %s returns the release notes of the %s model's builds that carry any,\n", f.buildsFuncName(), f.key)
	fmt.Fprintf(buf, "// generated from %s.\n", path.Base(f.manifestFile))
	fmt.Fprintf(buf, "func %s() []CatalogBuild {\n", f.buildsFuncName())
	if len(records) == 0 {
		buf.WriteString("\treturn nil\n}\n")
		return nil
	}
	buf.WriteString("\treturn []CatalogBuild{\n")
	for i := range records {
		r := &records[i]
		fmt.Fprintf(buf, "{Version: %q, Build: %d, Date: %q", r.version, r.build, r.date)
		if r.reason != "" {
			fmt.Fprintf(buf, ", Reason: %q", r.reason)
		}
		if len(r.notes) > 0 {
			buf.WriteString(", Notes: []string{\n")
			for _, n := range r.notes {
				fmt.Fprintf(buf, "%q,\n", n)
			}
			buf.WriteString("}")
		}
		buf.WriteString("},\n")
	}
	buf.WriteString("\t}\n}\n")
	return nil
}
