// Package recommend ranks the hardware variants of a model catalog entry
// against a host's detected capabilities, so the gallery can preselect the
// variant best suited to the machine while still letting the user override.
//
// It is deliberately pure: no I/O, no globals, no clock. The same Input always
// yields the same output, so the whole hardware matrix is table-testable
// without any hardware. The region axis reads Input.ResolvedRegion (resolved by
// the caller from the host coordinates and the ModelRegion setting); the engine
// itself performs no geometry.
package recommend

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
)

// Score terms. Kept as named constants (no magic numbers). The region terms
// (+100 matched / +50 global fallback) sit between the hard signals and the
// per-variant modifiers.
const (
	// scoreBackendRecommended is added when the best host-available backend is
	// the manifest's Recommended way to run the variant.
	scoreBackendRecommended = 40
	// scoreBackendSupported is added when the variant runs on a host-available
	// backend that is merely Supported, not Recommended.
	scoreBackendSupported = 10
	// scoreRegionMatched is added when the variant's regional slice matches the
	// host's resolved region. It is the strongest single term because a regional
	// slice is numerically identical to the global model on the species it keeps
	// while cutting peak RSS and latency, so a matching regional variant should
	// win over any global one.
	scoreRegionMatched = 100
	// scoreRegionGlobalFallback is added to a global variant when no regional
	// slice matches the host (either no region resolved, or the entry ships no
	// slice for the resolved region). It sits below scoreRegionMatched so a
	// matching regional variant always outranks the global one, and above the
	// per-variant precision modifiers so a wrong-region slice cannot outscore the
	// global model on those alone. (A wrong-region slice that also diverged from
	// the global sibling in backend support could in principle exceed this margin,
	// but a region slice mirrors its global sibling's precision and backend, so
	// that case does not arise in shipped catalog data.)
	scoreRegionGlobalFallback = 50
	// scoreFP16Native rewards an fp16 variant on a host with native
	// half-precision SIMD.
	scoreFP16Native = 15
	// scoreFP16GPUPreferredHeadroom is the margin above benchmarkMaxScore that
	// keeps fp16 winning outright rather than on the backend-rank tie-break.
	scoreFP16GPUPreferredHeadroom = 5
	// scoreFP16GPUPreferred keeps fp16 the preferred build when the variant's
	// best host path is a recommended GPU backend. fp16 is the deliberate size
	// lever for GPU hosts: half the download of fp32 and numerically equivalent
	// for this use, and running on the GPU frees the CPU for the audio pipeline,
	// so a CPU latency benchmark must not flip the pick to fp32. The value is
	// derived from benchmarkMaxScore, the largest advantage the latency term can
	// ever hand a sibling, plus a small headroom so fp16 wins outright instead of
	// leaning on the backend-rank tie-break. Like scoreRegionGlobalFallback, this
	// assumes a regional fp16 slice always has a global fp16 sibling (true in
	// shipped catalog data), so the term cannot promote a wrong-region slice past
	// every global variant.
	scoreFP16GPUPreferred = benchmarkMaxScore + scoreFP16GPUPreferredHeadroom
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

// ReasonArgBackend is the Args key naming the backend a backend.* reason refers to
// (e.g. {ReasonArgBackend: "openvino-gpu"}). Consumers read the chosen backend from
// this key; keeping it a shared constant stops the producer and its consumers
// drifting on the literal.
const ReasonArgBackend = "backend"

// Reason codes. Structured codes, never English sentences: the frontend maps
// each to an i18n key, so no user-facing wording lives in the backend.
const (
	// ReasonBackendRecommended marks the variant as running on its recommended
	// backend for this host. Arg "backend" names that backend.
	ReasonBackendRecommended = "backend.recommended"
	// ReasonBackendSupported marks the variant as running on a supported (not
	// recommended) backend for this host. Arg "backend" names that backend.
	ReasonBackendSupported = "backend.supported"
	// ReasonRegionMatched marks a regional variant matching the host's resolved
	// region. Arg "region" names the slug.
	ReasonRegionMatched = "region.matched"
	// ReasonRegionGlobalFallback marks the global variant chosen because no
	// regional slice matches the host.
	ReasonRegionGlobalFallback = "region.global_fallback"
	// ReasonPrecisionFP16Native marks an fp16 variant matched to native
	// half-precision SIMD.
	ReasonPrecisionFP16Native = "precision.fp16_native"
	// ReasonPrecisionFP16GPUPreferred marks an fp16 variant kept preferred
	// because its recommended backend on this host runs on a GPU.
	ReasonPrecisionFP16GPUPreferred = "precision.fp16_gpu_preferred"
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

	// backendCUDA and backendTensorRT are GPU backend tokens named here so
	// backendPreference and gpuBackendTokens share one definition rather than
	// repeating raw strings. The other backend tokens come from hwprofile.
	backendCUDA     = "cuda"
	backendTensorRT = "tensorrt"

	precisionFP16 = "fp16"
	precisionINT8 = "int8"

	bytesPerMB = 1 << 20
)

// backendPreference orders backends from most to least preferred, so a tie on
// score is broken toward the faster execution path. cuda and tensorrt lead the
// order for completeness; no BirdNET-Go build emits those host tokens yet, so
// in practice the decision is among the OpenVINO and ONNX Runtime backends.
var backendPreference = []string{
	backendCUDA,
	backendTensorRT,
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

// gpuBackendTokens are the backend tokens that execute inference on a GPU
// rather than the host CPU. Used to gate scoreFP16GPUPreferred, which keeps
// fp16 preferred only when the variant's recommended path is one of these.
// Keep this the GPU subset of backendPreference above: a new GPU backend added
// there must be added here too, or the fp16 size lever will not fire for it.
var gpuBackendTokens = []string{backendCUDA, backendTensorRT, hwprofile.CapOpenVINOGPU}

// isGPUBackend reports whether a backend token executes inference on a GPU.
func isGPUBackend(backend string) bool {
	return slices.Contains(gpuBackendTokens, backend)
}

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
	// ResolvedRegion is the region slug the host's coordinates resolved to under
	// the active ModelRegion mode, or "" when the global model applies (no
	// coordinates, ModeGlobal, or no tile contains the point). A variant whose
	// Region equals this slug is the regional match; a global variant (Region
	// "") earns the fallback bonus whenever no regional slice matches.
	ResolvedRegion string
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
// (no variants) produce nothing. Input is never mutated; it is taken by pointer
// only to avoid copying the (now region-aware) struct on every call.
func Rank(in *Input) []Recommendation {
	out := make([]Recommendation, 0)
	for i := range in.Entries {
		entry := &in.Entries[i]
		if len(entry.Variants) == 0 {
			continue
		}
		out = append(out, rankEntry(entry, in)...)
	}
	return out
}

// rankEntry ranks the variants of a single entry, marking the top compatible
// one Recommended.
func rankEntry(entry *classifier.CatalogEntry, in *Input) []Recommendation {
	bench := benchmarkScores(entry, in)

	// A global variant is the region fallback unless this entry actually ships a
	// slice for the resolved region. Computing it once here (rather than per
	// variant) keeps the global variant from earning the fallback bonus when a
	// matching regional slice exists, and keeps a wrong-region slice from
	// outscoring the global model on hardware terms when no slice matches.
	hasMatchingRegion := false
	if in.ResolvedRegion != "" {
		for j := range entry.Variants {
			if entry.Variants[j].Region == in.ResolvedRegion {
				hasMatchingRegion = true
				break
			}
		}
	}

	scoredVariants := make([]scoredVariant, 0, len(entry.Variants))
	for j := range entry.Variants {
		v := &entry.Variants[j]
		scoredVariants = append(scoredVariants, evaluateVariant(entry.ID, v, in, bench[v.ID], hasMatchingRegion))
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
// hasMatchingRegion reports whether the variant's entry ships a slice matching
// the host's resolved region, which decides whether a global variant earns the
// region fallback bonus (see rankEntry).
func evaluateVariant(catalogID string, v *classifier.CatalogVariant, in *Input, bench benchmarkResult, hasMatchingRegion bool) scoredVariant {
	var reasons, blockers []Reason
	score := 0

	// Hard filters. Each failure is a blocker; a variant with any blocker is
	// incompatible regardless of its score.
	if len(v.Requirements.Arch) > 0 && !anyPresent(v.Requirements.Arch, in.Capabilities) {
		blockers = append(blockers, Reason{Code: BlockerArchUnsupported, Args: joinArg("required", v.Requirements.Arch)})
	}
	// The explicit Requirements.Backends any-of filter and the backendTerm
	// candidates-empty check below both mean "this host reaches no usable backend
	// for the variant". A variant that trips both must still surface a single
	// backend.missing blocker, so the second occurrence is suppressed.
	backendMissing := false
	if len(v.Requirements.Backends) > 0 && !anyPresent(v.Requirements.Backends, in.Capabilities) {
		blockers = append(blockers, Reason{Code: BlockerBackendMissing, Args: joinArg("required", v.Requirements.Backends)})
		backendMissing = true
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
	// gpuRecommended is set when the variant's Recommended backend on this host
	// runs on a GPU, gating the fp16 size-lever modifier below.
	gpuRecommended := false
	if term, missing := backendTerm(v, in.Capabilities); missing {
		if !backendMissing {
			blockers = append(blockers, Reason{Code: BlockerBackendMissing, Args: joinArg("required", backendKeys(v.Backends))})
		}
	} else if term.applies {
		score += term.score
		reasons = append(reasons, Reason{Code: term.code, Args: map[string]string{ReasonArgBackend: term.backend}})
		backendRank = preferenceRank(term.backend)
		gpuRecommended = term.code == ReasonBackendRecommended && isGPUBackend(term.backend)
	}

	// Region term. A regional slice matching the host's resolved region is the
	// strongest correctness signal. A global variant earns the fallback bonus
	// whenever no slice matches the host (no region resolved, or the entry ships
	// no slice for the resolved region), so it always outranks a wrong-region
	// slice, which earns nothing here. Scored after the backend term so the
	// backend reason stays the headline (reasons[0]) the gallery renders.
	switch {
	case in.ResolvedRegion != "" && v.Region == in.ResolvedRegion:
		score += scoreRegionMatched
		reasons = append(reasons, Reason{Code: ReasonRegionMatched, Args: map[string]string{"region": v.Region}})
	case v.Region == "" && (in.ResolvedRegion == "" || !hasMatchingRegion):
		score += scoreRegionGlobalFallback
		reasons = append(reasons, Reason{Code: ReasonRegionGlobalFallback})
	}

	// Modifiers.
	if slices.Contains(in.Capabilities, hwprofile.CapFP16Native) && v.Precision == precisionFP16 {
		score += scoreFP16Native
		reasons = append(reasons, Reason{Code: ReasonPrecisionFP16Native})
	}
	// Keep fp16 the preferred build when its recommended backend on this host is
	// a GPU, so a faster CPU benchmark on the fp32 sibling cannot flip the pick
	// away from the deliberate GPU size lever (fp16 is half the download and
	// numerically equivalent here). See scoreFP16GPUPreferred. Scoped to x86: the
	// benchmark flip this counteracts arises only from the x86 CPU benchmark rows,
	// and CapOpenVINOGPU is not architecture-gated (hwprofile emits it from the
	// device probe alone), so requiring the x86 capability keeps the lever off any
	// non-x86 host, where its calibration has not been validated.
	if gpuRecommended && v.Precision == precisionFP16 &&
		slices.Contains(in.Capabilities, hwprofile.CapX86_64) {
		score += scoreFP16GPUPreferred
		reasons = append(reasons, Reason{Code: ReasonPrecisionFP16GPUPreferred})
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
		if !deviceMatches(b.Device, in.DeviceClass) || b.LatencyMs <= 0 || !slices.Contains(in.Capabilities, b.Backend) {
			continue
		}
		if !found || b.LatencyMs < best {
			best = b.LatencyMs
			found = true
		}
	}
	return best, found
}

// deviceMatches reports whether a benchmark's Device identifier applies to the
// host's device class. ARM classes name exact benchmark devices (rpi5-a76,
// rpi4b-a72), so they match by equality. The generic x86 class covers any
// x86-* benchmark device, because amd64 hosts are not binned into specific CPU
// models the way Pi board tiers are; the trailing hyphen keeps a token like
// "x8600" from matching.
func deviceMatches(benchDevice, deviceClass string) bool {
	if deviceClass == deviceClassX86 {
		return benchDevice == deviceClassX86 || strings.HasPrefix(benchDevice, deviceClassX86+"-")
	}
	return benchDevice == deviceClass
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
	slices.SortStableFunc(scoredVariants, func(x, y scoredVariant) int {
		if x.rec.Compatible != y.rec.Compatible {
			if x.rec.Compatible {
				return -1
			}
			return 1
		}
		// Score is ranked descending, so y is compared against x.
		if c := cmp.Compare(y.rec.Score, x.rec.Score); c != 0 {
			return c
		}
		if c := cmp.Compare(x.backendRank, y.backendRank); c != 0 {
			return c
		}
		if c := cmp.Compare(x.sizeBytes, y.sizeBytes); c != 0 {
			return c
		}
		return cmp.Compare(x.rec.VariantID, y.rec.VariantID)
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
