// Package recommend ranks the hardware variants of a model catalog entry
// against a host's detected capabilities, so the gallery can preselect the
// variant best suited to the machine while still letting the user override.
//
// It is deliberately pure: no I/O, no globals, no clock. The same Input always
// yields the same output, so the whole hardware matrix is table-testable
// without any hardware. The region axis is intentionally absent; a future region
// resolver will slot into the score without rescaling the terms defined here.
package recommend

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
)

// Score terms. Kept as named constants (no magic numbers) and spaced so the
// future region terms (+100 matched / +50 global fallback) slot between the
// hard signals and the modifiers without reordering anything.
const (
	// scoreBackendRecommended is added when the best host-available backend is
	// the manifest's Recommended way to run the variant.
	scoreBackendRecommended = 40
	// scoreBackendSupported is added when the variant runs on a host-available
	// backend that is merely Supported, not Recommended.
	scoreBackendSupported = 10
	// scoreFP16Native rewards an fp16 variant on a host with native
	// half-precision SIMD.
	scoreFP16Native = 15
	// scoreRAMConstrainedFit rewards an int8 variant on a memory-constrained
	// host, where the quantized build is the one that fits.
	scoreRAMConstrainedFit = 25
	// scoreLegacyPenalty demotes a superseded build far below any live variant
	// while still letting it be reported when it is the one installed.
	scoreLegacyPenalty = -1000
	// benchmarkMaxScore is the score of the fastest measured variant in an entry
	// when at least two variants carry a comparable benchmark.
	benchmarkMaxScore = 20
	// benchmarkNonMemberScore is the flat score of a variant with no comparable
	// benchmark, used only when other variants in the entry do have one.
	benchmarkNonMemberScore = 10
	// benchmarkMinMembers is the number of comparable benchmarks an entry needs
	// before the benchmark term contributes anything; below it the term is 0 for
	// every variant, because a single measurement cannot be scaled.
	benchmarkMinMembers = 2
)

// Reason codes. Structured codes, never English sentences: the frontend maps
// each to an i18n key, so no user-facing wording lives in the backend.
const (
	// ReasonBackendRecommended marks the variant as running on its recommended
	// backend for this host. Arg "backend" names that backend.
	ReasonBackendRecommended = "backend.recommended"
	// ReasonBackendSupported marks the variant as running on a supported (not
	// recommended) backend for this host. Arg "backend" names that backend.
	ReasonBackendSupported = "backend.supported"
	// ReasonPrecisionFP16Native marks an fp16 variant matched to native
	// half-precision SIMD.
	ReasonPrecisionFP16Native = "precision.fp16_native"
	// ReasonRAMConstrainedFit marks an int8 variant matched to a low-RAM host.
	ReasonRAMConstrainedFit = "ram.constrained_fit"
	// ReasonVariantLegacy marks a superseded build.
	ReasonVariantLegacy = "variant.legacy"
	// ReasonBenchmarkMeasured marks a variant scored from a real measurement on
	// this device class.
	ReasonBenchmarkMeasured = "benchmark.measured"
)

// Blocker codes. A variant with any blocker is incompatible with the host.
const (
	// BlockerArchUnsupported means the variant requires a CPU architecture the
	// host does not have. Arg "required" lists the accepted tokens.
	BlockerArchUnsupported = "arch.unsupported"
	// BlockerBackendMissing means the variant needs a backend the host cannot
	// reach, either by explicit requirement or because none of its supported
	// backends are available here. Arg "required" lists the accepted tokens.
	BlockerBackendMissing = "backend.missing"
	// BlockerRAMInsufficient means the host has less memory than the variant
	// needs. Arg "requiredMb" is the floor in MB.
	BlockerRAMInsufficient = "ram.insufficient"
	// BlockerHardwareExcluded means the host carries a capability token the
	// variant explicitly excludes (e.g. a known-bad GPU generation). Arg "token"
	// names it.
	BlockerHardwareExcluded = "hardware.excluded"
)

// Device classes, the benchmark Device identifiers a host maps to. Kept here
// rather than in the manifest because they are a property of the running host,
// derived from its board tier and architecture.
const (
	deviceClassRPi5 = "rpi5-a76"
	deviceClassRPi4 = "rpi4b-a72"
	deviceClassRPi3 = "rpi3b-a53"
	deviceClassX86  = "x86"

	boardTierPi5 = "pi5"
	boardTierPi4 = "pi4"
	boardTierPi3 = "pi3"

	archAMD64 = "amd64"

	precisionFP16 = "fp16"
	precisionINT8 = "int8"

	bytesPerMB = 1 << 20
)

// backendPreference orders backends from most to least preferred, so a tie on
// score is broken toward the faster execution path. cuda and tensorrt lead the
// order for completeness; no BirdNET-Go build emits those host tokens yet, so
// in practice the decision is among the OpenVINO and ONNX Runtime backends.
var backendPreference = []string{
	"cuda",
	"tensorrt",
	hwprofile.CapOpenVINOGPU,
	hwprofile.CapOpenVINOCPU,
	hwprofile.CapONNXRuntimeCPU,
	hwprofile.CapTFLite,
}

// backendRankUnavailable is the sort rank of a variant that resolved no
// host-available backend, so it sorts after every variant that did. It is a
// var only because len of a slice is not a compile-time constant; it is never
// reassigned.
var backendRankUnavailable = len(backendPreference)

// Reason is a structured, renderable explanation for a scoring decision or a
// hard-filter failure. Args carry the values the frontend interpolates into the
// localized string (e.g. {"backend": "openvino-gpu"}).
type Reason struct {
	Code string            `json:"code"`
	Args map[string]string `json:"args,omitempty"`
}

// Recommendation is the verdict for one variant of one catalog entry.
type Recommendation struct {
	// CatalogID and VariantID identify the variant this verdict is about.
	CatalogID string
	VariantID string
	// Score ranks variants within their entry; higher is better. It is only
	// meaningful relative to the other variants of the same entry.
	Score int
	// Compatible is true when the variant has no blockers, i.e. it can run on
	// this host.
	Compatible bool
	// Recommended is true for the single best compatible variant of its entry,
	// the one the gallery preselects. At most one per entry.
	Recommended bool
	// Reasons explain the score, for the gallery's "why" line.
	Reasons []Reason
	// Blockers explain incompatibility, for a disabled option's inline text.
	Blockers []Reason
}

// Input is the host profile plus the catalog to rank against.
type Input struct {
	// Capabilities are the host capability tokens (hwprofile.Profile.Capabilities()).
	Capabilities []string
	// TotalRAMBytes is the effective memory ceiling, 0 when unknown.
	TotalRAMBytes int64
	// DeviceClass is the benchmark device identifier for this host, "" when the
	// host maps to no benchmarked class.
	DeviceClass string
	// Entries is the visible catalog to evaluate.
	Entries []classifier.CatalogEntry
}

// DeviceClass maps a host's board tier and Go architecture to the benchmark
// Device identifier the manifests use, or "" when the host matches no
// benchmarked class (in which case the benchmark term contributes nothing).
func DeviceClass(boardTier, goArch string) string {
	switch boardTier {
	case boardTierPi5:
		return deviceClassRPi5
	case boardTierPi4:
		return deviceClassRPi4
	case boardTierPi3:
		return deviceClassRPi3
	}
	if goArch == archAMD64 {
		return deviceClassX86
	}
	return ""
}

// Rank evaluates every variant of every entry and returns the verdicts grouped
// by entry in input order, ranked best-first within each entry. Flat entries
// (no variants) produce nothing. Input is never mutated.
func Rank(in Input) []Recommendation {
	out := make([]Recommendation, 0)
	for i := range in.Entries {
		entry := &in.Entries[i]
		if len(entry.Variants) == 0 {
			continue
		}
		out = append(out, rankEntry(entry, &in)...)
	}
	return out
}

// rankEntry ranks the variants of a single entry, marking the top compatible
// one Recommended.
func rankEntry(entry *classifier.CatalogEntry, in *Input) []Recommendation {
	bench := benchmarkScores(entry, in)

	scoredVariants := make([]scoredVariant, 0, len(entry.Variants))
	for j := range entry.Variants {
		v := &entry.Variants[j]
		scoredVariants = append(scoredVariants, evaluateVariant(entry.ID, v, in, bench[v.ID]))
	}

	sortScored(scoredVariants)

	recs := make([]Recommendation, len(scoredVariants))
	recommendedChosen := false
	for k := range scoredVariants {
		rec := scoredVariants[k].rec
		// A Legacy (superseded) variant is never recommended for a fresh install:
		// the gallery hides it (buildVariantResponses) unless it is the installed
		// one, so recommending it would point at a variant absent from the client's
		// variant list. When the only compatible variant is Legacy, the entry gets
		// no recommendation, which is correct.
		if !recommendedChosen && rec.Compatible && !scoredVariants[k].legacy {
			rec.Recommended = true
			recommendedChosen = true
		}
		recs[k] = rec
	}
	return recs
}

// scoredVariant pairs a verdict with the sort keys that break ties among
// equally scored variants, plus the legacy flag that keeps a superseded build
// out of the Recommended slot.
type scoredVariant struct {
	rec         Recommendation
	backendRank int
	sizeBytes   int64
	legacy      bool
}

// evaluateVariant computes the verdict for one variant: its blockers (hard
// filters), its score terms, and the sort keys used to break ties.
func evaluateVariant(catalogID string, v *classifier.CatalogVariant, in *Input, bench benchmarkResult) scoredVariant {
	var reasons, blockers []Reason
	score := 0

	// Hard filters. Each failure is a blocker; a variant with any blocker is
	// incompatible regardless of its score.
	if len(v.Requirements.Arch) > 0 && !anyPresent(v.Requirements.Arch, in.Capabilities) {
		blockers = append(blockers, Reason{Code: BlockerArchUnsupported, Args: joinArg("required", v.Requirements.Arch)})
	}
	if len(v.Requirements.Backends) > 0 && !anyPresent(v.Requirements.Backends, in.Capabilities) {
		blockers = append(blockers, Reason{Code: BlockerBackendMissing, Args: joinArg("required", v.Requirements.Backends)})
	}
	// The RAM filter applies only when host RAM is known (TotalRAMBytes > 0).
	// When memory detection failed, hwprofile reports 0; an unknown RAM figure is
	// deliberately treated as "do not block", because hiding a model from a user
	// whose RAM probe failed is worse than optimistically offering it. The low-RAM
	// score bonus is likewise inert on unknown RAM, since its CapLowRAM token is
	// only emitted when TotalRAMBytes is both known and below the threshold.
	if v.Requirements.MinRAMMB > 0 && in.TotalRAMBytes > 0 && in.TotalRAMBytes < int64(v.Requirements.MinRAMMB)*bytesPerMB {
		blockers = append(blockers, Reason{Code: BlockerRAMInsufficient, Args: map[string]string{"requiredMb": strconv.Itoa(v.Requirements.MinRAMMB)}})
	}
	for _, ex := range v.Requirements.Excludes {
		if slices.Contains(in.Capabilities, ex) {
			blockers = append(blockers, Reason{Code: BlockerHardwareExcluded, Args: map[string]string{"token": ex}})
		}
	}

	// Backend term. Scores the variant on the best host-available backend and
	// records the rank used to break score ties.
	backendRank := backendRankUnavailable
	if term, missing := backendTerm(v, in.Capabilities); missing {
		blockers = append(blockers, Reason{Code: BlockerBackendMissing, Args: joinArg("required", backendKeys(v.Backends))})
	} else if term.applies {
		score += term.score
		reasons = append(reasons, Reason{Code: term.code, Args: map[string]string{"backend": term.backend}})
		backendRank = preferenceRank(term.backend)
	}

	// Modifiers.
	if slices.Contains(in.Capabilities, hwprofile.CapFP16Native) && v.Precision == precisionFP16 {
		score += scoreFP16Native
		reasons = append(reasons, Reason{Code: ReasonPrecisionFP16Native})
	}
	if slices.Contains(in.Capabilities, hwprofile.CapLowRAM) && v.Precision == precisionINT8 {
		score += scoreRAMConstrainedFit
		reasons = append(reasons, Reason{Code: ReasonRAMConstrainedFit})
	}
	if v.Legacy {
		score += scoreLegacyPenalty
		reasons = append(reasons, Reason{Code: ReasonVariantLegacy})
	}
	if bench.applies {
		score += bench.score
		if bench.member {
			reasons = append(reasons, Reason{Code: ReasonBenchmarkMeasured})
		}
	}

	return scoredVariant{
		rec: Recommendation{
			CatalogID:  catalogID,
			VariantID:  v.ID,
			Score:      score,
			Compatible: len(blockers) == 0,
			Reasons:    reasons,
			Blockers:   blockers,
		},
		backendRank: backendRank,
		sizeBytes:   variantSize(v),
		legacy:      v.Legacy,
	}
}

// backendTermResult is the outcome of scoring the backend axis for a variant.
type backendTermResult struct {
	applies bool   // a backend term was produced
	score   int    // scoreBackendRecommended or scoreBackendSupported
	code    string // the matching reason code
	backend string // the best host-available backend that was chosen
}

// backendTerm scores a variant on the best backend the host can reach. It
// returns missing=true when the variant declares backends but the host reaches
// none of them (a hard filter). A variant with no backend information at all
// (an empty map, as a user-edited catalog may carry) produces neither a term
// nor a blocker, so custom entries are never wrongly filtered.
func backendTerm(v *classifier.CatalogVariant, caps []string) (result backendTermResult, missing bool) {
	if len(v.Backends) == 0 {
		return backendTermResult{}, false
	}

	candidates := make([]string, 0, len(v.Backends))
	recommended := make([]string, 0, len(v.Backends))
	for backend, support := range v.Backends {
		if !support.Supported || !slices.Contains(caps, backend) {
			continue
		}
		candidates = append(candidates, backend)
		if support.Recommended {
			recommended = append(recommended, backend)
		}
	}

	if len(candidates) == 0 {
		return backendTermResult{}, true
	}

	// The best backend is chosen from the recommended set when it is non-empty,
	// never from the wider candidate set. Otherwise a variant merely Supported
	// (not Recommended) on a high-preference backend would score as if that were
	// its recommended path, mis-ranking it against a sibling that truly
	// recommends that backend.
	if len(recommended) > 0 {
		return backendTermResult{applies: true, score: scoreBackendRecommended, code: ReasonBackendRecommended, backend: preferredBackend(recommended)}, false
	}
	return backendTermResult{applies: true, score: scoreBackendSupported, code: ReasonBackendSupported, backend: preferredBackend(candidates)}, false
}

// benchmarkResult is the benchmark term for one variant within its entry.
type benchmarkResult struct {
	applies bool // the entry had enough comparable benchmarks for the term to count
	member  bool // this variant carried a comparable benchmark
	score   int
}

// benchmarkScores computes the benchmark term for every variant of an entry.
// The term compares only benchmarks measured on the host's device class with a
// backend the host can reach. When fewer than benchmarkMinMembers variants have
// such a benchmark, the term is zero for all, because a lone measurement cannot
// be scaled into a ranking.
func benchmarkScores(entry *classifier.CatalogEntry, in *Input) map[string]benchmarkResult {
	out := make(map[string]benchmarkResult, len(entry.Variants))
	if in.DeviceClass == "" {
		return out
	}

	latencies := make(map[string]int, len(entry.Variants))
	for j := range entry.Variants {
		v := &entry.Variants[j]
		if lat, ok := comparableLatency(v, in); ok {
			latencies[v.ID] = lat
		}
	}
	if len(latencies) < benchmarkMinMembers {
		return out
	}

	minLat, maxLat := latencyRange(latencies)
	for j := range entry.Variants {
		v := &entry.Variants[j]
		lat, member := latencies[v.ID]
		if !member {
			out[v.ID] = benchmarkResult{applies: true, member: false, score: benchmarkNonMemberScore}
			continue
		}
		out[v.ID] = benchmarkResult{applies: true, member: true, score: scaleBenchmark(lat, minLat, maxLat)}
	}
	return out
}

// comparableLatency returns the smallest latency this variant measured on the
// host's device class with a host-reachable backend.
func comparableLatency(v *classifier.CatalogVariant, in *Input) (latency int, ok bool) {
	best := 0
	found := false
	for i := range v.Benchmarks {
		b := &v.Benchmarks[i]
		if b.Device != in.DeviceClass || b.LatencyMs <= 0 || !slices.Contains(in.Capabilities, b.Backend) {
			continue
		}
		if !found || b.LatencyMs < best {
			best = b.LatencyMs
			found = true
		}
	}
	return best, found
}

// scaleBenchmark maps a latency to a score in [0, benchmarkMaxScore], fastest
// highest. When every member shares one latency the range is degenerate and all
// members are equally best.
func scaleBenchmark(latency, minLat, maxLat int) int {
	if maxLat == minLat {
		return benchmarkMaxScore
	}
	return benchmarkMaxScore * (maxLat - latency) / (maxLat - minLat)
}

// latencyRange returns the min and max of a non-empty latency set.
func latencyRange(latencies map[string]int) (minLat, maxLat int) {
	first := true
	for _, lat := range latencies {
		if first {
			minLat, maxLat = lat, lat
			first = false
			continue
		}
		if lat < minLat {
			minLat = lat
		}
		if lat > maxLat {
			maxLat = lat
		}
	}
	return minLat, maxLat
}

// sortScored orders variants best-first: compatible before blocked, then higher
// score, then better backend rank, then smaller download, then variant id.
func sortScored(scoredVariants []scoredVariant) {
	sort.SliceStable(scoredVariants, func(a, b int) bool {
		x, y := &scoredVariants[a], &scoredVariants[b]
		if x.rec.Compatible != y.rec.Compatible {
			return x.rec.Compatible
		}
		if x.rec.Score != y.rec.Score {
			return x.rec.Score > y.rec.Score
		}
		if x.backendRank != y.backendRank {
			return x.backendRank < y.backendRank
		}
		if x.sizeBytes != y.sizeBytes {
			return x.sizeBytes < y.sizeBytes
		}
		return x.rec.VariantID < y.rec.VariantID
	})
}

// preferredBackend returns the highest-preference backend among the given set.
// The candidate slices are built by ranging a map, so ties on preference rank
// (two backends outside backendPreference, both ranking backendRankUnavailable)
// are broken lexically to keep Rank deterministic regardless of map order.
func preferredBackend(backends []string) string {
	best := backends[0]
	bestRank := preferenceRank(best)
	for _, b := range backends[1:] {
		if r := preferenceRank(b); r < bestRank || (r == bestRank && b < best) {
			best, bestRank = b, r
		}
	}
	return best
}

// preferenceRank returns the sort rank of a backend (lower is preferred), or
// backendRankUnavailable for a backend outside the known order.
func preferenceRank(backend string) int {
	if i := slices.Index(backendPreference, backend); i >= 0 {
		return i
	}
	return backendRankUnavailable
}

// backendKeys returns the sorted supported-backend keys of a backend support
// map, for a stable "required" argument on the backend.missing blocker. Only
// Supported backends are listed: a key marked Supported:false is not a viable
// target, so naming it as "required" would mislead.
func backendKeys(backends map[string]classifier.BackendSupport) []string {
	keys := make([]string, 0, len(backends))
	for backend, support := range backends {
		if support.Supported {
			keys = append(keys, backend)
		}
	}
	slices.Sort(keys)
	return keys
}

// variantSize sums the download size of a variant's files.
func variantSize(v *classifier.CatalogVariant) int64 {
	var total int64
	for i := range v.Files {
		total += v.Files[i].SizeBytes
	}
	return total
}

// anyPresent reports whether any of the required tokens is present in have.
func anyPresent(required, have []string) bool {
	for _, r := range required {
		if slices.Contains(have, r) {
			return true
		}
	}
	return false
}

// joinArg builds a single-entry arg map joining tokens with commas.
func joinArg(key string, tokens []string) map[string]string {
	return map[string]string{key: strings.Join(tokens, ",")}
}
