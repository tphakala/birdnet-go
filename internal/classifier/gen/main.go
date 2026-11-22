// Command gen generates the regional model variants embedded by the classifier
// package (internal/classifier/model_catalog_regional_gen.go). It reads the
// vendored acoustic-models manifests (gen/manifests/*.models.json), the embedded
// region tables (region/data/*.regions.json), and a labels-checksum sidecar
// (gen/manifests/labels-checksums.json), then emits one Go file with one
// function per family (birdnetV30RegionalVariants, perchV2RegionalVariants and
// birdnetPlusV30RegionalVariants), each returning the region-sliced
// CatalogVariant literals for its family (the family's expectedRegions slugs,
// each with one live build of every token in family.liveVariants; a build the
// manifest supersedes stays in the list as a Legacy variant, so an existing
// install is still recognised, and sorts after its region's live builds). The
// manifest's build number is part of a variant's identity: build 1 keeps the
// "<variant>@<region>" id and the pre-build file names, every later build adds
// "-b<N>" to the id and "_b<N>" before the file extension, so a build never
// shares a file name with another build and an update can download next to the
// running model. A superseded build is chained to the live build of its tile.
// A family that hosts
// several upstream releases (family.releases) lists every release's builds: the
// first release keeps the bare "<variant>@<region>" id and its file names, each
// later release carries "-<release>" in its id and "v<release>" in its file
// names, and an older release is a Legacy variant that names its successor. When
// the upstream manifest no longer lists an older release, the generator reads it
// from a frozen copy (family.frozenManifests, gen/manifests/releases/) and chains
// each of its entries to the current manifest's entry of the same region and
// variant.
//
// The generator takes the per-region labels checksums from the sidecar rather
// than from the manifests (older manifests hashed only the .onnx model files).
// Run "go run ./gen -update-labels-checksums" (the only networked mode) to
// refresh that sidecar from HuggingFace; normal "go generate" is fully offline
// so the CI drift gate needs no network.
//
// Perch v2 regional entries must carry a score_calibration block (the rest logit
// the classifier adds to the slice softmax so slice scores match the full model's
// scale); the generator hard-fails when it is missing, unknown or out of range,
// and when a manifest labels_sha256 disagrees with the checksum sidecar, so a
// constant can never be attached to a different labels file than it was fitted on.
//
// Determinism matters: the output is committed and guarded by a CI drift gate,
// so the generator must produce byte-identical output for a given input. Entries
// are sorted by (region slug, live before superseded, variant token, release,
// build), backend map keys are emitted sorted, the field order is fixed, and the
// whole file is run through go/format.Source before writing.
//
// Bootstrap safety: this program is package main and does NOT import
// internal/classifier, so a broken generated file never blocks regenerating it.
//
// A second output, model_catalog_documents_gen.go, carries the licence and
// policy documents of every vendored manifest (gen/manifests/*.models.json and
// the document-only gen/manifests/documents/*.models.json) as a registry keyed
// by repo; see documents.go.
//
// Run via: go generate ./internal/classifier
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// families is the set of model families whose regional tiles are generated. Each
// names its vendored manifest, its embedded region table, and how it maps a
// manifest variant token to a stable on-disk LocalName. LocalNames must stay
// stable across catalog updates: ScanInstalled keys installs on LocalName, so a
// rename would strand an existing install. A LocalName is a pure function of the
// entry's own release and build: build 1 keeps the names that shipped before
// builds were tracked (so every existing install is recognised), a later build
// adds "_b<N>" before the extension, so one build's file can never sit under
// another build's or release's name.
var families = []family{
	{
		key:          "birdnet-v3.0",
		funcName:     "birdnetV30RegionalVariants",
		repo:         "tphakala/BirdNET-v3.0-Models",
		manifestFile: "gen/manifests/BirdNET-v3.0-Models.models.json",
		regionsFile:  "region/data/BirdNET-v3.0-Models.regions.json",
		// birdnet_v3.0_<slug>_<token>.onnx for the model, one shared
		// birdnet_v3.0_<slug>_labels.txt per region (both precisions share it).
		modelLocalName: func(slug, token, _ string, build int) string {
			return fmt.Sprintf("birdnet_v3.0_%s_%s%s.onnx", slug, token, buildSuffix(build))
		},
		labelsLocalName: func(slug, _ string, build int) string {
			return fmt.Sprintf("birdnet_v3.0_%s_labels%s.txt", slug, buildSuffix(build))
		},
		liveVariants:    []string{"fp16", "fp32"},
		expectedRegions: 43,
	},
	{
		key:          "perch-v2",
		funcName:     "perchV2RegionalVariants",
		repo:         "tphakala/Perch-v2-Models",
		manifestFile: "gen/manifests/Perch-v2-Models.models.json",
		regionsFile:  "region/data/Perch-v2-Models.regions.json",
		// Softmax over the loaded labels: a slice needs its rest logit.
		requireScoreCalibration: true,
		// perch_v2_<slug>_<infix>.onnx, matching the global naming
		// (perch_v2_no_dft.onnx, perch_v2_int8_arm.onnx).
		modelLocalName: func(slug, token, _ string, build int) string {
			return fmt.Sprintf("perch_v2_%s_%s%s.onnx", slug, perchInfix(token), buildSuffix(build))
		},
		labelsLocalName: func(slug, _ string, build int) string {
			return fmt.Sprintf("perch_v2_%s_labels%s.txt", slug, buildSuffix(build))
		},
		liveVariants: []string{"no-dft-fp32"},
		// 43 tiles plus central-india.
		expectedRegions: 44,
	},
	{
		key:          "birdnet-plus-v3.0",
		funcName:     "birdnetPlusV30RegionalVariants",
		repo:         "tphakala/BirdNET-Plus-V3.0",
		manifestFile: "gen/manifests/BirdNET-Plus-V3.0.models.json",
		regionsFile:  "region/data/BirdNET-Plus-V3.0.regions.json",
		// birdnet_plus_v<release>_<slug>_<token>.onnx for the model, one
		// birdnet_plus_v<release>_<slug>_labels.txt per region, release and build.
		// Only fp32 ships. The 3.0 names and the 3.0.1 build 1 names are the ones
		// installed builds already carry.
		modelLocalName: func(slug, token, release string, build int) string {
			return fmt.Sprintf("birdnet_plus_v%s_%s_%s%s.onnx", release, slug, token, buildSuffix(build))
		},
		labelsLocalName: func(slug, release string, build int) string {
			return fmt.Sprintf("birdnet_plus_v%s_%s_labels%s.txt", release, slug, buildSuffix(build))
		},
		liveVariants: []string{"fp32"},
		// 43 tiles plus central-india, which has no 3.0 build.
		expectedRegions:     44,
		olderReleaseRegions: 43,
		// 3.0 is hosted here from a frozen copy because the upstream manifest
		// lists 3.0.1 only; every 3.0 build stays a Legacy variant of its 3.0.1
		// successor so an installed 3.0 is still recognised and repairable.
		releases:        []string{"3.0", "3.0.1"},
		frozenManifests: []string{"gen/manifests/releases/BirdNET-Plus-V3.0.release-3.0.models.json"},
	},
}

// buildSuffix is the file name suffix of a build: none for build 1, whose names
// predate build tracking, "_b<N>" for a later build.
func buildSuffix(build int) string { return buildTag("_", build) }

// idBuildSuffix is the variant id suffix of a build: none for build 1, "-b<N>"
// for a later build.
func idBuildSuffix(build int) string { return buildTag("-", build) }

// buildTag is "<sep>b<N>" for a build above 1 and "" for build 1.
func buildTag(sep string, build int) string {
	if build <= 1 {
		return ""
	}
	return fmt.Sprintf("%sb%d", sep, build)
}

// entryBuild is the entry's build number within its release, 1 when the manifest
// does not say (build() refuses a regional entry without one before any id is
// derived, so only hand-built test entries take the default).
func entryBuild(e *manifestModel) int {
	if e.Build == nil {
		return 1
	}
	return *e.Build
}

// perchInfix maps a Perch manifest variant token to its LocalName infix, matching
// the hand-written global entries.
func perchInfix(token string) string {
	switch token {
	case "int8-arm":
		return "int8_arm"
	case "no-dft-fp32":
		return "no_dft"
	default:
		return strings.ReplaceAll(token, "-", "_")
	}
}

type family struct {
	key          string
	funcName     string
	repo         string
	manifestFile string
	regionsFile  string
	// requireScoreCalibration marks a family whose scores depend on the label set
	// (Perch v2 softmax). A family with per-class sigmoid scores (BirdNET v3.0)
	// must not carry the field.
	requireScoreCalibration bool
	// expectedRegions is the number of region tiles the family ships. It is
	// hand-maintained on purpose: an upstream tile addition or removal must be
	// acknowledged here at generate time, together with the golden tier snapshot
	// in the region package. Do not derive it from the region table, which would
	// make the count check vacuous. Families may differ: a family can lag the
	// shared tile set (BirdNET v3.0, the superseded preview, has no central-india
	// tile).
	expectedRegions int
	// liveVariants are the variant tokens every region must ship exactly one live
	// (not superseded) build of, and no other token: fp16 plus fp32 for BirdNET
	// v3.0, no-dft-fp32 for Perch, fp32 for BirdNET+. Superseded builds are not
	// counted, their number varies per tile.
	liveVariants []string
	// releases lists every upstream release the family hosts, oldest first, empty
	// for a one-release family. Entries of a release outside the list are refused
	// (checkReleases), and the number of regions that carry each older release is
	// pinned by olderReleaseRegions.
	releases []string
	// olderReleaseRegions is the number of regions that carry builds of each older
	// release in releases (0 for a one-release family). Hand-maintained like
	// expectedRegions: a newer tile may lack the older release (BirdNET+ central-india
	// has no 3.0 build), so the count is not expectedRegions, and an older release
	// dropping out of the frozen inputs for a region cannot pass unnoticed.
	olderReleaseRegions int
	// frozenManifests are vendored, verbatim copies of older releases' manifests
	// that the upstream manifest no longer lists. Their regional entries are
	// appended and chained to the current manifest by mergeFrozenReleases.
	frozenManifests []string
	modelLocalName  func(slug, token, release string, build int) string
	labelsLocalName func(slug, release string, build int) string
}

// versionPattern is the shape of an upstream release string: it lands in a file
// name and an id, so only digits and dots are accepted.
var versionPattern = regexp.MustCompile(`^\d+(\.\d+)*$`)

// currentRelease is the newest listed release, or "" for a one-release family.
func (f *family) currentRelease() string {
	if len(f.releases) == 0 {
		return ""
	}
	return f.releases[len(f.releases)-1]
}

// variantID is the catalog id of the entry's variant: "<variant>@<region>" for a
// one-release family and for the first release of a multi-release one, so an id
// never changes meaning, else "<variant>-<release>@<region>"; a build above 1
// adds "-b<N>" to the variant part ("fp32-3.0.1-b2@nordic"), so build 1 keeps
// the id every existing install is recognised by.
func (f *family) variantID(e *manifestModel) string {
	token := e.Variant
	if len(f.releases) != 0 && e.UpstreamVersion != f.releases[0] {
		token += "-" + e.UpstreamVersion
	}
	return token + idBuildSuffix(entryBuild(e)) + "@" + e.Region
}

// manifestFiles lists every manifest the family's regional entries come from.
func (f *family) manifestFiles() []string {
	return append([]string{f.manifestFile}, f.frozenManifests...)
}

// mergeFrozenReleases appends the frozen manifests' regional entries to current
// and chains each to the live current-release entry of the same region and
// variant (its superseded_by), the way an upstream manifest hosting both releases
// would. Superseded builds of the current release are never a chain target, so
// the frozen entry points at the build a new install uses. A frozen entry that
// already names a successor, belongs to the current release, reuses a listed
// path, or has no live current-release entry to chain to is refused, and so is a
// second live current-release entry for one region and variant.
func (f *family) mergeFrozenReleases(current, frozen []manifestModel) ([]manifestModel, error) {
	type key struct{ region, variant string }
	successor := make(map[key]*manifestModel, len(current))
	listed := make(map[string]bool, len(current)+len(frozen))
	for i := range current {
		listed[current[i].Path] = true
		if current[i].UpstreamVersion != f.currentRelease() || current[i].SupersededBy != nil {
			continue
		}
		k := key{current[i].Region, current[i].Variant}
		if successor[k] != nil {
			return nil, fmt.Errorf("two live current-release entries for %s@%s", k.variant, k.region)
		}
		successor[k] = &current[i]
	}
	out := slices.Clone(current)
	for i := range frozen {
		e := frozen[i]
		id := e.Variant + "@" + e.Region
		switch {
		case e.UpstreamVersion == f.currentRelease():
			return nil, fmt.Errorf("frozen entry %s: release %s is the current release, which the current manifest must list", id, e.UpstreamVersion)
		case e.SupersededBy != nil:
			return nil, fmt.Errorf("frozen entry %s already names a successor; the generator chains frozen entries itself", id)
		case listed[e.Path]:
			return nil, fmt.Errorf("frozen entry %s: path %q is already listed", id, e.Path)
		}
		next, ok := successor[key{e.Region, e.Variant}]
		if !ok {
			return nil, fmt.Errorf("frozen entry %s: no current-release entry of that region and variant to supersede it", id)
		}
		listed[e.Path] = true
		e.SupersededBy = new(next.Path)
		out = append(out, e)
	}
	return out, nil
}

// checkReleases enforces the multi-release rules on the merged regional entries:
// every upstream_version is a plain version the family lists, an entry of an older
// release names its successor (so it stays a Legacy variant and an installed build
// is never offered as new), and the manifest's current_version is the newest
// listed release. A one-release family is not checked.
func (f *family) checkReleases(regional []manifestModel, currentVersion string) error {
	if len(f.releases) == 0 {
		return nil
	}
	if currentVersion != f.currentRelease() {
		return fmt.Errorf("manifest current_version %q is not the newest listed release %q", currentVersion, f.currentRelease())
	}
	for i := range regional {
		e := &regional[i]
		if !versionPattern.MatchString(e.UpstreamVersion) {
			return fmt.Errorf("entry %s: upstream_version %q is not a plain version (digits and dots)", f.variantID(e), e.UpstreamVersion)
		}
		if !slices.Contains(f.releases, e.UpstreamVersion) {
			return fmt.Errorf("entry %s: release %q is not one of %v", f.variantID(e), e.UpstreamVersion, f.releases)
		}
		if e.UpstreamVersion != f.currentRelease() && e.SupersededBy == nil {
			return fmt.Errorf("entry %s: release %s must name its successor (superseded_by), or an installed build would be offered as new", f.variantID(e), e.UpstreamVersion)
		}
	}
	return nil
}

// checkRegionCounts enforces the live-token rule on the merged regional
// entries: the distinct regions must number exactly the family's expectedRegions,
// and each region must ship exactly one live (not superseded) build of every
// token in liveVariants and of no other token. For a multi-release family every
// live entry must also be of the current release, and each older release must be
// carried by exactly olderReleaseRegions regions, so a region losing its older
// builds cannot strand an installed one. Superseded builds are otherwise not
// counted (their number varies per tile), so a dropped, duplicated or extra live
// tile cannot pass unnoticed even when the totals still match, and every region
// keeps a build a new install can use.
func (f *family) checkRegionCounts(regional []manifestModel) error {
	live := make(map[string][]string, f.expectedRegions) // region -> live variant tokens
	olderRegions := make(map[string]map[string]bool, 1)  // older release -> regions carrying it
	for i := range regional {
		e := &regional[i]
		if _, ok := live[e.Region]; !ok {
			live[e.Region] = nil
		}
		if len(f.releases) > 0 && e.UpstreamVersion != f.currentRelease() {
			if olderRegions[e.UpstreamVersion] == nil {
				olderRegions[e.UpstreamVersion] = map[string]bool{}
			}
			olderRegions[e.UpstreamVersion][e.Region] = true
		}
		if e.SupersededBy != nil {
			continue
		}
		if len(f.releases) > 0 && e.UpstreamVersion != f.currentRelease() {
			return fmt.Errorf("region %s: live entry %s is of release %s, want the current release %s", e.Region, f.variantID(e), e.UpstreamVersion, f.currentRelease())
		}
		live[e.Region] = append(live[e.Region], e.Variant)
	}
	if len(live) != f.expectedRegions {
		return fmt.Errorf("regional entries cover %d distinct regions, want %d (a region is missing or duplicated)", len(live), f.expectedRegions)
	}
	if len(f.releases) > 1 {
		for _, release := range f.releases[:len(f.releases)-1] {
			if got := len(olderRegions[release]); got != f.olderReleaseRegions {
				return fmt.Errorf("release %s is carried by %d regions, want %d (an older release must keep its builds in every region that had it)", release, got, f.olderReleaseRegions)
			}
		}
	}
	want := slices.Sorted(slices.Values(f.liveVariants))
	for region, tokens := range live {
		slices.Sort(tokens)
		if !slices.Equal(tokens, want) {
			return fmt.Errorf("region %s has live builds %v, want exactly one each of %v", region, tokens, want)
		}
	}
	return nil
}

// manifest and its nested types mirror only the acoustic-models schema-2 fields
// this generator consumes. Unknown fields are ignored.
type manifest struct {
	// Repo is the HuggingFace repository the manifest describes; the document
	// registry is keyed by it.
	Repo       string             `json:"repo"`
	FamilyMeta manifestFamilyMeta `json:"family_meta"`
	Models     []manifestModel    `json:"models"`
}

type manifestModel struct {
	Path       string                     `json:"path"`
	Variant    string                     `json:"variant"`
	Precision  string                     `json:"precision"`
	Region     string                     `json:"region"`
	Classes    *int                       `json:"classes"`
	Labels     string                     `json:"labels"`
	Backends   map[string]manifestBackend `json:"backends"`
	Benchmarks []manifestBenchmark        `json:"benchmarks"`
	SHA256     string                     `json:"sha256"`
	SizeBytes  int64                      `json:"size_bytes"`
	// UpstreamVersion is the upstream release the entry's files belong to.
	UpstreamVersion string `json:"upstream_version"`
	// Build is the build number of the entry's files within its release; a
	// pointer so a missing value is told apart from zero. See entryBuild.
	Build        *int                  `json:"build"`
	SupersededBy *string               `json:"superseded_by"`
	Requirements *manifestRequirements `json:"requirements"`
	// LabelsSHA256 is the checksum of the entry's labels file; when set it must
	// equal the sidecar checksum, and a family that requires a score calibration
	// needs it set.
	LabelsSHA256     string                    `json:"labels_sha256"`
	ScoreCalibration *manifestScoreCalibration `json:"score_calibration"`
}

// manifestScoreCalibration mirrors the manifest's per-entry score_calibration block.
// RestLogit is a pointer so a missing value is told apart from zero.
type manifestScoreCalibration struct {
	Method    string   `json:"method"`
	RestLogit *float64 `json:"rest_logit"`
}

type manifestBackend struct {
	Supported   bool `json:"supported"`
	Recommended bool `json:"recommended"`
}

type manifestBenchmark struct {
	Device    string `json:"device"`
	Backend   string `json:"backend"`
	LatencyMs int    `json:"latency_ms"`
	RSSMB     int    `json:"rss_mb"`
}

type manifestRequirements struct {
	MinRAMMB int      `json:"min_ram_mb"`
	Excludes []string `json:"excludes"`
}

// regionTable mirrors the embedded region table, used for slug validation and
// (for Perch, whose manifest omits classes) the per-region species count.
type regionTable struct {
	Regions map[string]struct {
		Classes *int `json:"classes"`
	} `json:"regions"`
}

// labelChecksum is one entry in the labels-checksum sidecar.
type labelChecksum struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Lines     int    `json:"lines"`
}

const (
	// scoreCalibrationMethod, scoreCalibrationRestLogitMin and Max mirror
	// ScoreCalibrationSoftmaxRestLogit and the bounds in model_catalog.go (this
	// program cannot import the package it generates for) and the
	// acoustic-models validator.
	scoreCalibrationMethod       = "softmax-rest-logit"
	scoreCalibrationRestLogitMin = 8.0
	scoreCalibrationRestLogitMax = 12.0

	labelsSidecar = "gen/manifests/labels-checksums.json"
	outputFile    = "model_catalog_regional_gen.go"
)

func main() {
	update := flag.Bool("update-labels-checksums", false, "fetch every regional labels file from HuggingFace and rewrite the labels-checksum sidecar (networked)")
	flag.Parse()

	if *update {
		if err := updateLabelsSidecar(); err != nil {
			log.Fatalf("gen-model-catalog: update labels checksums: %v", err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatalf("gen-model-catalog: %v", err)
	}
}

func run() error {
	labels, err := loadLabelsSidecar()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.WriteString(fileHeader)
	total := 0
	for i := range families {
		fam := &families[i]
		variants, genErr := fam.build(labels)
		if genErr != nil {
			return fmt.Errorf("family %s: %w", fam.key, genErr)
		}
		total += len(variants)
		fmt.Fprintf(&buf, "\n// %s returns the %d region-sliced variants of the %s model,\n", fam.funcName, len(variants), fam.key)
		fmt.Fprintf(&buf, "// generated from %s.\n", path.Base(fam.manifestFile))
		fmt.Fprintf(&buf, "func %s() []CatalogVariant {\n\treturn []CatalogVariant{\n", fam.funcName)
		for _, v := range variants {
			buf.WriteString(v)
		}
		buf.WriteString("\t}\n}\n")
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("gofmt generated source: %w", err)
	}
	if err := os.WriteFile(outputFile, formatted, 0o644); err != nil { //nolint:gosec // generated, non-secret source file
		return fmt.Errorf("write %s: %w", outputFile, err)
	}
	fmt.Printf("wrote %s (%d variants across %d families)\n", outputFile, total, len(families))
	return runDocuments()
}

// regionalEntries returns the manifest's regional (non-global) entries.
func regionalEntries(m *manifest) []manifestModel {
	var out []manifestModel
	for i := range m.Models {
		if m.Models[i].Region != "" {
			out = append(out, m.Models[i])
		}
	}
	return out
}

// build parses the family's manifest (plus its frozen older-release manifests,
// chained to the current one) and region table and returns the rendered Go source
// for each regional variant, sorted by region, then live builds before superseded
// ones, and resolves each superseded_by path to a variant id. It hard-fails on
// any data gap, and on a multi-release family that does not host every listed
// release in every region, so a malformed manifest can never silently emit a
// broken catalog.
func (f *family) build(labels map[string]labelChecksum) ([]string, error) {
	m, err := loadManifest(f.manifestFile)
	if err != nil {
		return nil, err
	}
	table, err := loadRegionTable(f.regionsFile)
	if err != nil {
		return nil, err
	}

	regional := regionalEntries(m)
	if len(f.frozenManifests) > 0 {
		var frozen []manifestModel
		for _, file := range f.frozenManifests {
			fm, loadErr := loadManifest(file)
			if loadErr != nil {
				return nil, loadErr
			}
			frozen = append(frozen, regionalEntries(fm)...)
		}
		if regional, err = f.mergeFrozenReleases(regional, frozen); err != nil {
			return nil, err
		}
	}
	for i := range regional {
		if b := regional[i].Build; b == nil || *b < 1 {
			return nil, fmt.Errorf("variant %s: missing build", f.variantID(&regional[i]))
		}
	}
	if err := f.checkReleases(regional, m.FamilyMeta.CurrentVersion); err != nil {
		return nil, err
	}
	if len(table.Regions) != f.expectedRegions {
		return nil, fmt.Errorf("region table has %d slugs, want %d", len(table.Regions), f.expectedRegions)
	}
	if err := f.checkRegionCounts(regional); err != nil {
		return nil, err
	}

	// Deterministic order: region slug, then live before superseded, then variant
	// token, then release, then build (the keys that make the order total). The
	// runtime walks over a catalog entry's variants order live builds first
	// themselves; this order keeps the generated file stable and readable.
	sort.Slice(regional, func(i, j int) bool {
		if regional[i].Region != regional[j].Region {
			return regional[i].Region < regional[j].Region
		}
		if li, lj := regional[i].SupersededBy == nil, regional[j].SupersededBy == nil; li != lj {
			return li
		}
		if regional[i].Variant != regional[j].Variant {
			return regional[i].Variant < regional[j].Variant
		}
		if regional[i].UpstreamVersion != regional[j].UpstreamVersion {
			return regional[i].UpstreamVersion < regional[j].UpstreamVersion
		}
		return entryBuild(&regional[i]) < entryBuild(&regional[j])
	})

	// Index after the sort: the pointers must address the final slice positions.
	byPath := make(map[string]*manifestModel, len(regional))
	for i := range regional {
		byPath[regional[i].Path] = &regional[i]
	}

	seenID := make(map[string]bool, len(regional))
	seenLocalName := make(map[string]bool, len(regional))
	seenLabels := make(map[string]labelsUse, len(regional))
	out := make([]string, 0, len(regional))
	for i := range regional {
		e := &regional[i]
		v, buildErr := f.variant(e, table, labels)
		if buildErr != nil {
			return nil, buildErr
		}
		if e.SupersededBy != nil {
			if v.supersededBy, buildErr = f.supersededVariantID(e, byPath); buildErr != nil {
				return nil, buildErr
			}
		}
		if seenID[v.id] {
			return nil, fmt.Errorf("duplicate variant id %q", v.id)
		}
		if seenLocalName[v.modelLocalName] {
			return nil, fmt.Errorf("duplicate model LocalName %q", v.modelLocalName)
		}
		// The precisions of one build share a labels file, so a repeat is fine
		// when release, build and checksum all match; two builds must never share
		// a labels file name (invariant: different builds never share a LocalName).
		use := labelsUse{release: e.UpstreamVersion, build: entryBuild(e), sha256: v.labelsSHA256}
		if prev, dup := seenLabels[v.labelsLocal]; dup && prev != use {
			return nil, fmt.Errorf("variant %s: labels LocalName %q is shared with another release, build or labels file", v.id, v.labelsLocal)
		}
		seenLabels[v.labelsLocal] = use
		seenID[v.id] = true
		seenLocalName[v.modelLocalName] = true
		out = append(out, render(&v))
	}
	return out, nil
}

// labelsUse identifies the labels file behind a labels LocalName: the release and
// build of the entry that uses it and the file's checksum.
type labelsUse struct {
	release string
	build   int
	sha256  string
}

// genVariant is the resolved, validated data for one regional variant, ready to
// render.
type genVariant struct {
	id           string
	region       string
	precision    string
	speciesCount int
	arch         []string
	minRAMMB     int
	excludes     []string
	backends     []backendRow
	benchmarks   []manifestBenchmark
	legacy       bool
	supersededBy string
	// version is the entry's release when it is not the family's newest, else "".
	version string
	// build is the entry's build number, 1 or more; rendered only above 1.
	build          int
	modelRemote    string
	modelLocalName string
	modelSHA256    string
	modelSize      int64
	labelsRemote   string
	labelsLocal    string
	labelsSHA256   string
	labelsSize     int64
	// restLogit is the validated score calibration, meaningful only when
	// hasCalibration is set.
	restLogit      float64
	hasCalibration bool
}

type backendRow struct {
	token       string
	recommended bool
}

func (f *family) variant(e *manifestModel, table *regionTable, labels map[string]labelChecksum) (genVariant, error) {
	id := f.variantID(e)
	if _, ok := table.Regions[e.Region]; !ok {
		return genVariant{}, fmt.Errorf("variant %s: region slug not in the embedded region table", id)
	}
	if e.Path == "" || e.SHA256 == "" || e.SizeBytes <= 0 {
		return genVariant{}, fmt.Errorf("variant %s: missing model path/sha256/size", id)
	}
	if e.Requirements == nil || e.Requirements.MinRAMMB <= 0 {
		return genVariant{}, fmt.Errorf("variant %s: missing requirements.min_ram_mb", id)
	}

	// Species count: manifest classes for v3.0, region-table classes for Perch
	// (whose manifest omits the field). When both exist they must agree, or the
	// manifest and the embedded region table have drifted.
	// The region table describes the live build, so a superseded entry takes its
	// own manifest classes and is not compared with the table.
	tableClasses := table.Regions[e.Region].Classes
	species := 0
	switch {
	case e.SupersededBy != nil:
		if e.Classes == nil {
			return genVariant{}, fmt.Errorf("variant %s: superseded entry must list classes", id)
		}
		species = *e.Classes
	case e.Classes != nil:
		species = *e.Classes
		if tableClasses != nil && *tableClasses != species {
			return genVariant{}, fmt.Errorf("variant %s: manifest classes %d != region table classes %d", id, species, *tableClasses)
		}
	case tableClasses != nil:
		species = *tableClasses
	}
	if species <= 0 {
		return genVariant{}, fmt.Errorf("variant %s: no species count from manifest classes or region table", id)
	}

	labelKey := f.repo + "|" + e.Labels
	lc, ok := labels[labelKey]
	if !ok {
		return genVariant{}, fmt.Errorf("variant %s: no labels checksum for %q (run -update-labels-checksums)", id, labelKey)
	}
	if lc.SHA256 == "" || lc.SizeBytes <= 0 {
		return genVariant{}, fmt.Errorf("variant %s: labels checksum for %q missing sha256/size", id, labelKey)
	}
	if lc.Lines != species {
		return genVariant{}, fmt.Errorf("variant %s: labels line count %d != species count %d", id, lc.Lines, species)
	}

	if e.LabelsSHA256 == "" && f.requireScoreCalibration {
		return genVariant{}, fmt.Errorf("variant %s: manifest labels_sha256 is missing, so its score_calibration cannot be tied to a labels file", id)
	}
	if e.LabelsSHA256 != "" && e.LabelsSHA256 != lc.SHA256 {
		return genVariant{}, fmt.Errorf("variant %s: manifest labels_sha256 %s != labels checksum sidecar %s for %q", id, e.LabelsSHA256, lc.SHA256, labelKey)
	}
	restLogit, hasCalibration, err := f.scoreCalibration(e)
	if err != nil {
		return genVariant{}, err
	}

	build := entryBuild(e)
	var arch []string
	if e.Variant == "int8-arm" {
		arch = []string{"aarch64"}
	}
	var excludes []string
	if e.Requirements != nil {
		excludes = e.Requirements.Excludes
	}

	backends := make([]backendRow, 0, len(e.Backends))
	for token, b := range e.Backends {
		if !b.Supported {
			continue // keep only supported rows, matching the hand-written globals
		}
		backends = append(backends, backendRow{token: token, recommended: b.Recommended})
	}
	sort.Slice(backends, func(i, j int) bool { return backends[i].token < backends[j].token })

	return genVariant{
		id:           id,
		region:       e.Region,
		precision:    normalizePrecision(e.Precision),
		speciesCount: species,
		arch:         arch,
		minRAMMB:     e.Requirements.MinRAMMB,
		excludes:     excludes,
		backends:     backends,
		benchmarks:   e.Benchmarks,
		// Legacy is derived from superseded_by only; build fills supersededBy with
		// the resolved variant id. The manifest's own `legacy`
		// bool means "published before the current naming scheme" (frozen
		// filename), NOT "superseded"; CatalogVariant.Legacy means the recommender
		// hides and penalizes the variant, so passing the manifest flag through
		// would wrongly bury every regional tile.
		legacy:         e.SupersededBy != nil,
		modelRemote:    e.Path,
		modelLocalName: f.modelLocalName(e.Region, e.Variant, e.UpstreamVersion, build),
		modelSHA256:    e.SHA256,
		modelSize:      e.SizeBytes,
		labelsRemote:   e.Labels,
		labelsLocal:    f.labelsLocalName(e.Region, e.UpstreamVersion, build),
		labelsSHA256:   lc.SHA256,
		labelsSize:     lc.SizeBytes,
		restLogit:      restLogit,
		hasCalibration: hasCalibration,
		version:        f.olderRelease(e),
		build:          build,
	}, nil
}

// olderRelease returns the entry's release when the family lists a newer one, or
// "" for the newest release and for a one-release family.
func (f *family) olderRelease(e *manifestModel) string {
	if len(f.releases) == 0 || e.UpstreamVersion == f.currentRelease() {
		return ""
	}
	return e.UpstreamVersion
}

// supersededVariantID turns the entry's superseded_by manifest path into the id
// of the variant that replaces it (variantID of the target), or "" when the entry
// is not superseded. The target must be a regional entry of the same family and
// region that is not itself superseded, so the pointer never dangles, crosses
// regions or chains.
func (f *family) supersededVariantID(e *manifestModel, byPath map[string]*manifestModel) (string, error) {
	if e.SupersededBy == nil {
		return "", nil
	}
	id := f.variantID(e)
	target, ok := byPath[*e.SupersededBy]
	switch {
	case !ok:
		return "", fmt.Errorf("variant %s: superseded_by %q is not an entry of this family", id, *e.SupersededBy)
	case target.Region != e.Region:
		return "", fmt.Errorf("variant %s: superseded_by %q is in a different region (%s)", id, *e.SupersededBy, target.Region)
	case target.SupersededBy != nil:
		return "", fmt.Errorf("variant %s: superseded_by %q is itself superseded", id, *e.SupersededBy)
	}
	return f.variantID(target), nil
}

// scoreCalibration validates the entry's score_calibration block against the
// family's rule. It returns the rest logit and true, or false when the family takes
// no calibration (and the entry correctly carries none).
func (f *family) scoreCalibration(e *manifestModel) (restLogit float64, has bool, err error) {
	if !f.requireScoreCalibration {
		if e.ScoreCalibration != nil {
			return 0, false, fmt.Errorf("variant %s@%s: score_calibration is not allowed for family %s", e.Variant, e.Region, f.key)
		}
		return 0, false, nil
	}
	sc := e.ScoreCalibration
	if sc == nil || sc.RestLogit == nil {
		return 0, false, fmt.Errorf("variant %s@%s: missing score_calibration (publish the calibration in the manifest first)", e.Variant, e.Region)
	}
	if sc.Method != scoreCalibrationMethod {
		return 0, false, fmt.Errorf("variant %s@%s: unknown score_calibration method %q", e.Variant, e.Region, sc.Method)
	}
	v := *sc.RestLogit
	if math.IsNaN(v) || math.IsInf(v, 0) || v < scoreCalibrationRestLogitMin || v > scoreCalibrationRestLogitMax {
		return 0, false, fmt.Errorf("variant %s@%s: score_calibration rest_logit %v outside [%v, %v]", e.Variant, e.Region, v, scoreCalibrationRestLogitMin, scoreCalibrationRestLogitMax)
	}
	return v, true, nil
}

// normalizePrecision maps manifest precision tokens to the recommender's
// vocabulary (it compares Precision == "int8"/"fp16").
func normalizePrecision(p string) string {
	if p == "int8-arm" {
		return "int8"
	}
	return p
}

// render emits the Go source for one CatalogVariant literal, including its
// ScoreCalibration when the variant has one, its Version when it belongs to
// an older release than the family's newest and its Build when it is above 1. gofmt fixes the indentation
// afterwards, so this only needs valid syntax.
func render(v *genVariant) string {
	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "ID: %q,\n", v.id)
	fmt.Fprintf(&b, "Region: %q,\n", v.region)
	fmt.Fprintf(&b, "Precision: %q,\n", v.precision)
	fmt.Fprintf(&b, "SpeciesCount: %d,\n", v.speciesCount)

	b.WriteString("Requirements: VariantRequirements{")
	var reqs []string
	if len(v.arch) > 0 {
		reqs = append(reqs, fmt.Sprintf("Arch: %#v", v.arch))
	}
	reqs = append(reqs, fmt.Sprintf("MinRAMMB: %d", v.minRAMMB))
	if len(v.excludes) > 0 {
		reqs = append(reqs, fmt.Sprintf("Excludes: %#v", v.excludes))
	}
	b.WriteString(strings.Join(reqs, ", "))
	b.WriteString("},\n")

	if len(v.backends) > 0 {
		b.WriteString("Backends: map[string]BackendSupport{\n")
		for _, row := range v.backends {
			if row.recommended {
				fmt.Fprintf(&b, "%q: {Supported: true, Recommended: true},\n", row.token)
			} else {
				fmt.Fprintf(&b, "%q: {Supported: true},\n", row.token)
			}
		}
		b.WriteString("},\n")
	}

	if len(v.benchmarks) > 0 {
		b.WriteString("Benchmarks: []Benchmark{\n")
		for _, bm := range v.benchmarks {
			b.WriteString("{")
			fmt.Fprintf(&b, "Device: %q, Backend: %q", bm.Device, bm.Backend)
			if bm.LatencyMs > 0 {
				fmt.Fprintf(&b, ", LatencyMs: %d", bm.LatencyMs)
			}
			if bm.RSSMB > 0 {
				fmt.Fprintf(&b, ", RSSMB: %d", bm.RSSMB)
			}
			b.WriteString("},\n")
		}
		b.WriteString("},\n")
	}

	if v.legacy {
		b.WriteString("Legacy: true,\n")
	}
	if v.supersededBy != "" {
		fmt.Fprintf(&b, "SupersededBy: %q,\n", v.supersededBy)
	}
	if v.version != "" {
		fmt.Fprintf(&b, "Version: %q,\n", v.version)
	}
	if v.build > 1 {
		fmt.Fprintf(&b, "Build: %d,\n", v.build)
	}

	if v.hasCalibration {
		fmt.Fprintf(&b, "ScoreCalibration: &ScoreCalibration{Method: ScoreCalibrationSoftmaxRestLogit, RestLogit: %s},\n",
			strconv.FormatFloat(v.restLogit, 'f', -1, 64))
	}

	b.WriteString("Files: slices.Concat([]CatalogFile{\n")
	fmt.Fprintf(&b, "{RemotePath: %q, LocalName: %q, Role: RoleModel, SHA256: %q, SizeBytes: %d},\n",
		v.modelRemote, v.modelLocalName, v.modelSHA256, v.modelSize)
	fmt.Fprintf(&b, "{RemotePath: %q, LocalName: %q, Role: RoleLabels, SHA256: %q, SizeBytes: %d},\n",
		v.labelsRemote, v.labelsLocal, v.labelsSHA256, v.labelsSize)
	b.WriteString("}, taxonomyFiles()),\n")

	b.WriteString("},\n")
	return b.String()
}

func loadManifest(pathname string) (*manifest, error) {
	data, err := os.ReadFile(filepath.Clean(pathname))
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", pathname, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %q: %w", pathname, err)
	}
	return &m, nil
}

func loadRegionTable(pathname string) (*regionTable, error) {
	data, err := os.ReadFile(filepath.Clean(pathname))
	if err != nil {
		return nil, fmt.Errorf("read region table %q: %w", pathname, err)
	}
	var t regionTable
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse region table %q: %w", pathname, err)
	}
	return &t, nil
}

func loadLabelsSidecar() (map[string]labelChecksum, error) {
	data, err := os.ReadFile(labelsSidecar)
	if err != nil {
		return nil, fmt.Errorf("read labels sidecar %q (run -update-labels-checksums): %w", labelsSidecar, err)
	}
	var out map[string]labelChecksum
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse labels sidecar %q: %w", labelsSidecar, err)
	}
	return out, nil
}

// updateLabelsSidecar fetches every regional labels file of every family's
// manifests, frozen older releases included, from HuggingFace, hashes the exact
// bytes the download path will fetch, and rewrites the sidecar. This is
// the only networked mode; it is run by hand when the labels change, and its
// output is committed.
func updateLabelsSidecar() error {
	client := &http.Client{Timeout: 60 * time.Second}
	out := map[string]labelChecksum{}
	for i := range families {
		fam := &families[i]
		for _, file := range fam.manifestFiles() {
			m, err := loadManifest(file)
			if err != nil {
				return err
			}
			entries := regionalEntries(m)
			for j := range entries {
				e := &entries[j]
				if e.Labels == "" {
					continue
				}
				key := fam.repo + "|" + e.Labels
				if _, done := out[key]; done {
					continue // labels are shared across a region's precisions
				}
				lc, fetchErr := fetchLabelChecksum(client, fam.repo, e.Labels)
				if fetchErr != nil {
					return fmt.Errorf("fetch %s: %w", key, fetchErr)
				}
				out[key] = lc
				fmt.Printf("hashed %s (%d lines, %d bytes)\n", key, lc.Lines, lc.SizeBytes)
			}
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(labelsSidecar, data, 0o644); err != nil { //nolint:gosec // generated, non-secret sidecar
		return err
	}
	fmt.Printf("wrote %s (%d labels files)\n", labelsSidecar, len(out))
	return nil
}

func fetchLabelChecksum(client *http.Client, repo, remotePath string) (labelChecksum, error) {
	url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repo, remotePath)
	resp, err := client.Get(url) //nolint:noctx // one-shot maintenance tool
	if err != nil {
		return labelChecksum{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return labelChecksum{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return labelChecksum{}, err
	}
	sum := sha256.Sum256(body)
	lines := bytes.Count(body, []byte{'\n'})
	if len(body) > 0 && !bytes.HasSuffix(body, []byte{'\n'}) {
		lines++
	}
	return labelChecksum{
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(body)),
		Lines:     lines,
	}, nil
}

const fileHeader = `// Code generated by "go run ./gen" from gen/manifests/*.models.json; DO NOT EDIT.

package classifier

import "slices"
`
