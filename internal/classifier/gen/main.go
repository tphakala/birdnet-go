// Command gen generates the regional model variants embedded by the classifier
// package (internal/classifier/model_catalog_regional_gen.go). It reads the
// vendored acoustic-models manifests (gen/manifests/*.models.json), the embedded
// region tables (region/data/*.regions.json), and a labels-checksum sidecar
// (gen/manifests/labels-checksums.json), then emits one Go file with two
// functions, birdnetV30RegionalVariants and perchV2RegionalVariants, each
// returning the 78 region-sliced CatalogVariant literals for its family.
//
// The manifests carry sha256/size only for the .onnx model files (the upstream
// make_manifest scripts hash .onnx only), so the per-region labels files get
// their checksums from the sidecar. Run "go run ./gen -update-labels-checksums"
// (the only networked mode) to refresh that sidecar from HuggingFace; normal
// "go generate" is fully offline so the CI drift gate needs no network.
//
// Determinism matters: the output is committed and guarded by a CI drift gate,
// so the generator must produce byte-identical output for a given input. Entries
// are sorted by (region slug, variant token), backend map keys are emitted
// sorted, the field order is fixed, and the whole file is run through
// go/format.Source before writing.
//
// Bootstrap safety: this program is package main and does NOT import
// internal/classifier, so a broken generated file never blocks regenerating it.
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
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// families is the set of model families whose regional tiles are generated. Each
// names its vendored manifest, its embedded region table, and how it maps a
// manifest variant token to a stable on-disk LocalName. LocalNames must stay
// stable across catalog updates: ScanInstalled keys installs on LocalName, so a
// rename would strand an existing install.
var families = []family{
	{
		key:          "birdnet-v3.0",
		funcName:     "birdnetV30RegionalVariants",
		repo:         "tphakala/BirdNET-v3.0-Models",
		manifestFile: "gen/manifests/BirdNET-v3.0-Models.models.json",
		regionsFile:  "region/data/BirdNET-v3.0-Models.regions.json",
		// birdnet_v3.0_<slug>_<token>.onnx for the model, one shared
		// birdnet_v3.0_<slug>_labels.txt per region (both precisions share it).
		modelLocalName: func(slug, token string) string {
			return fmt.Sprintf("birdnet_v3.0_%s_%s.onnx", slug, token)
		},
		labelsLocalName: func(slug string) string {
			return fmt.Sprintf("birdnet_v3.0_%s_labels.txt", slug)
		},
	},
	{
		key:          "perch-v2",
		funcName:     "perchV2RegionalVariants",
		repo:         "tphakala/Perch-v2-Models",
		manifestFile: "gen/manifests/Perch-v2-Models.models.json",
		regionsFile:  "region/data/Perch-v2-Models.regions.json",
		// perch_v2_<slug>_<infix>.onnx, matching the global naming
		// (perch_v2_no_dft.onnx, perch_v2_int8_arm.onnx).
		modelLocalName: func(slug, token string) string {
			return fmt.Sprintf("perch_v2_%s_%s.onnx", slug, perchInfix(token))
		},
		labelsLocalName: func(slug string) string {
			return fmt.Sprintf("perch_v2_%s_labels.txt", slug)
		},
	},
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
	key             string
	funcName        string
	repo            string
	manifestFile    string
	regionsFile     string
	modelLocalName  func(slug, token string) string
	labelsLocalName func(slug string) string
}

// manifest and its nested types mirror only the acoustic-models schema-2 fields
// this generator consumes. Unknown fields are ignored.
type manifest struct {
	Models []manifestModel `json:"models"`
}

type manifestModel struct {
	Path         string                     `json:"path"`
	Variant      string                     `json:"variant"`
	Precision    string                     `json:"precision"`
	Region       string                     `json:"region"`
	Classes      *int                       `json:"classes"`
	Labels       string                     `json:"labels"`
	Backends     map[string]manifestBackend `json:"backends"`
	Benchmarks   []manifestBenchmark        `json:"benchmarks"`
	SHA256       string                     `json:"sha256"`
	SizeBytes    int64                      `json:"size_bytes"`
	SupersededBy *string                    `json:"superseded_by"`
	Requirements *manifestRequirements      `json:"requirements"`
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
	labelsSidecar     = "gen/manifests/labels-checksums.json"
	outputFile        = "model_catalog_regional_gen.go"
	expectedRegions   = 40
	variantsPerRegion = 2 // each region ships two precision variants
	regionalPerFam    = expectedRegions * variantsPerRegion
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
	for _, fam := range families {
		variants, genErr := fam.build(labels)
		if genErr != nil {
			return fmt.Errorf("family %s: %w", fam.key, genErr)
		}
		if len(variants) != regionalPerFam {
			return fmt.Errorf("family %s: got %d regional variants, want %d", fam.key, len(variants), regionalPerFam)
		}
		fmt.Fprintf(&buf, "\n// %s returns the %d region-sliced variants of the %s model,\n", fam.funcName, regionalPerFam, fam.key)
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
	fmt.Printf("wrote %s (%d variants across %d families)\n", outputFile, len(families)*regionalPerFam, len(families))
	return nil
}

// build parses the family's manifest and region table and returns the rendered
// Go source for each regional variant, sorted deterministically. It hard-fails on
// any data gap so a malformed manifest can never silently emit a broken catalog.
func (f *family) build(labels map[string]labelChecksum) ([]string, error) {
	m, err := loadManifest(f.manifestFile)
	if err != nil {
		return nil, err
	}
	table, err := loadRegionTable(f.regionsFile)
	if err != nil {
		return nil, err
	}

	regional := make([]manifestModel, 0, regionalPerFam)
	for i := range m.Models {
		if m.Models[i].Region != "" {
			regional = append(regional, m.Models[i])
		}
	}
	if len(table.Regions) != expectedRegions {
		return nil, fmt.Errorf("region table has %d slugs, want %d", len(table.Regions), expectedRegions)
	}
	distinctRegions := make(map[string]bool, expectedRegions)
	for i := range regional {
		distinctRegions[regional[i].Region] = true
	}
	if len(distinctRegions) != expectedRegions {
		return nil, fmt.Errorf("regional entries cover %d distinct regions, want %d (a region is missing or duplicated)", len(distinctRegions), expectedRegions)
	}

	// Deterministic order: region slug, then variant token.
	sort.Slice(regional, func(i, j int) bool {
		if regional[i].Region != regional[j].Region {
			return regional[i].Region < regional[j].Region
		}
		return regional[i].Variant < regional[j].Variant
	})

	seenID := make(map[string]bool, len(regional))
	seenLocalName := make(map[string]bool, len(regional))
	out := make([]string, 0, len(regional))
	for i := range regional {
		e := &regional[i]
		v, buildErr := f.variant(e, table, labels)
		if buildErr != nil {
			return nil, buildErr
		}
		if seenID[v.id] {
			return nil, fmt.Errorf("duplicate variant id %q", v.id)
		}
		if seenLocalName[v.modelLocalName] {
			return nil, fmt.Errorf("duplicate model LocalName %q", v.modelLocalName)
		}
		seenID[v.id] = true
		seenLocalName[v.modelLocalName] = true
		out = append(out, render(&v))
	}
	return out, nil
}

// genVariant is the resolved, validated data for one regional variant, ready to
// render.
type genVariant struct {
	id             string
	region         string
	precision      string
	speciesCount   int
	arch           []string
	minRAMMB       int
	excludes       []string
	backends       []backendRow
	benchmarks     []manifestBenchmark
	legacy         bool
	supersededBy   string
	modelRemote    string
	modelLocalName string
	modelSHA256    string
	modelSize      int64
	labelsRemote   string
	labelsLocal    string
	labelsSHA256   string
	labelsSize     int64
}

type backendRow struct {
	token       string
	recommended bool
}

func (f *family) variant(e *manifestModel, table *regionTable, labels map[string]labelChecksum) (genVariant, error) {
	if _, ok := table.Regions[e.Region]; !ok {
		return genVariant{}, fmt.Errorf("variant %s@%s: region slug not in the embedded region table", e.Variant, e.Region)
	}
	if e.Path == "" || e.SHA256 == "" || e.SizeBytes <= 0 {
		return genVariant{}, fmt.Errorf("variant %s@%s: missing model path/sha256/size", e.Variant, e.Region)
	}
	if e.Requirements == nil || e.Requirements.MinRAMMB <= 0 {
		return genVariant{}, fmt.Errorf("variant %s@%s: missing requirements.min_ram_mb", e.Variant, e.Region)
	}

	// Species count: manifest classes for v3.0, region-table classes for Perch
	// (whose manifest omits the field). When both exist they must agree, or the
	// manifest and the embedded region table have drifted.
	tableClasses := table.Regions[e.Region].Classes
	species := 0
	switch {
	case e.Classes != nil:
		species = *e.Classes
		if tableClasses != nil && *tableClasses != species {
			return genVariant{}, fmt.Errorf("variant %s@%s: manifest classes %d != region table classes %d", e.Variant, e.Region, species, *tableClasses)
		}
	case tableClasses != nil:
		species = *tableClasses
	}
	if species <= 0 {
		return genVariant{}, fmt.Errorf("variant %s@%s: no species count from manifest classes or region table", e.Variant, e.Region)
	}

	labelKey := f.repo + "|" + e.Labels
	lc, ok := labels[labelKey]
	if !ok {
		return genVariant{}, fmt.Errorf("variant %s@%s: no labels checksum for %q (run -update-labels-checksums)", e.Variant, e.Region, labelKey)
	}
	if lc.SHA256 == "" || lc.SizeBytes <= 0 {
		return genVariant{}, fmt.Errorf("variant %s@%s: labels checksum for %q missing sha256/size", e.Variant, e.Region, labelKey)
	}
	if lc.Lines != species {
		return genVariant{}, fmt.Errorf("variant %s@%s: labels line count %d != species count %d", e.Variant, e.Region, lc.Lines, species)
	}

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

	superseded := ""
	if e.SupersededBy != nil {
		superseded = *e.SupersededBy
	}

	return genVariant{
		id:           e.Variant + "@" + e.Region,
		region:       e.Region,
		precision:    normalizePrecision(e.Precision),
		speciesCount: species,
		arch:         arch,
		minRAMMB:     e.Requirements.MinRAMMB,
		excludes:     excludes,
		backends:     backends,
		benchmarks:   e.Benchmarks,
		// Legacy is derived from supersededBy only. The manifest's own `legacy`
		// bool means "published before the current naming scheme" (frozen
		// filename), NOT "superseded"; CatalogVariant.Legacy means the recommender
		// hides and penalizes the variant, so passing the manifest flag through
		// would wrongly bury every regional tile.
		legacy:         superseded != "",
		supersededBy:   superseded,
		modelRemote:    e.Path,
		modelLocalName: f.modelLocalName(e.Region, e.Variant),
		modelSHA256:    e.SHA256,
		modelSize:      e.SizeBytes,
		labelsRemote:   e.Labels,
		labelsLocal:    f.labelsLocalName(e.Region),
		labelsSHA256:   lc.SHA256,
		labelsSize:     lc.SizeBytes,
	}, nil
}

// normalizePrecision maps manifest precision tokens to the recommender's
// vocabulary (it compares Precision == "int8"/"fp16").
func normalizePrecision(p string) string {
	if p == "int8-arm" {
		return "int8"
	}
	return p
}

// render emits the Go source for one CatalogVariant literal. gofmt fixes the
// indentation afterwards, so this only needs valid syntax.
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

	b.WriteString("Files: slices.Concat([]CatalogFile{\n")
	fmt.Fprintf(&b, "{RemotePath: %q, LocalName: %q, Role: RoleModel, SHA256: %q, SizeBytes: %d},\n",
		v.modelRemote, v.modelLocalName, v.modelSHA256, v.modelSize)
	fmt.Fprintf(&b, "{RemotePath: %q, LocalName: %q, Role: RoleLabels, SHA256: %q, SizeBytes: %d},\n",
		v.labelsRemote, v.labelsLocal, v.labelsSHA256, v.labelsSize)
	b.WriteString("}, geomodelFiles(), taxonomyFiles()),\n")

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

// updateLabelsSidecar fetches every regional labels file from HuggingFace, hashes
// the exact bytes the download path will fetch, and rewrites the sidecar. This is
// the only networked mode; it is run by hand when the labels change, and its
// output is committed.
func updateLabelsSidecar() error {
	client := &http.Client{Timeout: 60 * time.Second}
	out := map[string]labelChecksum{}
	for _, fam := range families {
		m, err := loadManifest(fam.manifestFile)
		if err != nil {
			return err
		}
		for i := range m.Models {
			e := &m.Models[i]
			if e.Region == "" || e.Labels == "" {
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
