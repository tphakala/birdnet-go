// Package models is the api/v2 models domain handler. It owns the
// /api/v2/models/* endpoints: listing the enabled classifier models, browsing
// the model gallery catalog, and installing, reinstalling, uninstalling, and
// streaming download progress for gallery models. The Handler embeds
// *apicore.Core by pointer so the shared dependencies and helpers (ModelManager,
// CurrentSettings, HandleError, the Go/Context goroutine plumbing, and the
// logging helpers) promote onto it; the facade constructs one Handler and calls
// RegisterRoutes to wire the routes in their existing order.
package models

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Handler serves the models domain endpoints. It embeds *apicore.Core BY
// POINTER so the shared Core members promote onto it without re-wiring; Core
// carries atomic/lock-bearing fields and must never be copied by value.
type Handler struct {
	*apicore.Core
	// authService gates the hardware-derived recommendation fields on the public
	// catalog endpoint to authenticated (or auth-not-required) requests, so an
	// anonymous caller on an auth-enabled instance cannot read the host's hardware
	// profile. nil in tests and when WithAuthService was not injected, in which
	// case enrichment is computed unconditionally (there is no auth boundary to
	// protect).
	authService auth.Service
	// hardwareProfile resolves the host inference profile for the recommender. It
	// is an injectable seam: nil means "use the default live probe"
	// (defaultHardwareProfile), and tests set it to synthetic hardware. A
	// per-Handler field rather than a package global, so tests stay parallel-safe.
	// It receives the request's already-probed ONNX Runtime status so the default
	// probe does not re-check ORT that GetModelCatalog just checked.
	hardwareProfile func(ort inference.ORTStatus) hwprofile.Profile
}

// New builds a models Handler around the shared core and the facade-injected
// auth service. The auth service is read only to gate the catalog endpoint's
// hardware-recommendation fields; every other dependency (ModelManager,
// settings, error/log helpers, goroutine plumbing) promotes from *apicore.Core.
func New(core *apicore.Core, authService auth.Service) *Handler {
	return &Handler{Core: core, authService: authService}
}

// RegisterRoutes registers all model-related API endpoints on the supplied API
// v2 group, preserving the exact routes and order the facade used before the
// models domain was extracted.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	g.GET("/models", c.ListModels)
	g.GET("/models/catalog", c.GetModelCatalog)
	g.GET("/models/regions", c.GetModelRegions, c.AuthMiddleware)
	// Public: serves fixed embedded SVG bytes selected by a public region slug,
	// nothing user-derived, matching the public /models/catalog route.
	g.GET("/models/regions/:slug/map", c.GetRegionCoverageMap)
	g.GET("/models/installed", c.GetInstalledModels)
	g.POST("/models/install/:id", c.InstallModel, c.AuthMiddleware)
	g.POST("/models/reinstall/:id", c.ReinstallModel, c.AuthMiddleware)
	g.DELETE("/models/installed/:id", c.UninstallModel, c.AuthMiddleware)
	g.GET("/models/install/:id/progress", c.StreamInstallProgress)
}

// ModelListItem represents a model in the API response.
type ModelListItem struct {
	ID                    string `json:"id"`                              // Config alias (e.g., "birdnet", "perch_v2")
	Name                  string `json:"name"`                            // Display name (e.g., "BirdNET v2.4 (TFLite)")
	Category              string `json:"category"`                        // Model category (e.g., "bird", "bat")
	MinSampleRate         int    `json:"minSampleRate,omitempty"`         // Minimum required sample rate in Hz
	RecommendedSampleRate int    `json:"recommendedSampleRate,omitempty"` // Recommended sample rate in Hz
}

// CatalogEntryResponse represents a model in the catalog API response.
type CatalogEntryResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Author        string `json:"author"`
	License       string `json:"license"`
	CommercialUse bool   `json:"commercialUse"`
	Category      string `json:"category"`
	Region        string `json:"region"`
	SpeciesCount  int    `json:"speciesCount"`
	Version       string `json:"version"`
	// Channel is the release channel: "stable" for a GA build, "preview" for a
	// developer-preview build the gallery flags as not the final release. Always
	// populated (defaults to "stable") so the frontend never has to guess.
	Channel string `json:"channel"`
	// BuildLabel is a human-facing build tag shown next to Version for a non-stable
	// channel (e.g. "preview3.1"); omitted for stable releases.
	BuildLabel  string `json:"buildLabel,omitempty"`
	UpstreamURL string `json:"upstreamUrl,omitempty"`
	Installed   bool   `json:"installed"`
	Compatible  bool   `json:"compatible"`
	// IncompatibleReason is a structured, localizable code (an i18n key stem such
	// as "backend.onnx_unavailable"), never a raw English message, so the fully
	// i18n'd gallery can translate it; a client renders it through the same reason
	// path as the variant reasons, falling back to the code. The specific
	// underlying error (e.g. which ONNX Runtime library was missing) is not in
	// this passive listing; it is returned in full when the user attempts to
	// install the model.
	IncompatibleReason string `json:"incompatibleReason,omitempty"`
	TotalSizeBytes     int64  `json:"totalSizeBytes"`
	HasGeomodel        bool   `json:"hasGeomodel"`
	// Permanent marks the built-in BirdNET v2.4 classifier: always installed, never
	// uninstallable, only its variant may be swapped. The gallery renders a built-in
	// badge instead of Remove/Reinstall for it.
	Permanent bool `json:"permanent,omitempty"`
	// InstalledVariantID is the id of the currently installed variant, or "" when
	// the model is not installed or is a flat (pre-variant) entry.
	InstalledVariantID string `json:"installedVariantId,omitempty"`
	// Variants lists the selectable hardware/regional variants of this model,
	// omitted for flat single-variant entries.
	Variants []CatalogVariantResponse `json:"variants,omitempty"`
	// RecommendedVariantID is the variant the gallery preselects for this host.
	// Omitted for flat entries and when the request is not eligible for hardware
	// recommendations (an unauthenticated request on an auth-enabled instance).
	RecommendedVariantID string `json:"recommendedVariantId,omitempty"`
}

// CatalogVariantResponse describes one selectable hardware or regional variant of
// a catalog entry for the gallery UI.
type CatalogVariantResponse struct {
	ID           string `json:"id"`
	Region       string `json:"region,omitempty"`
	Precision    string `json:"precision,omitempty"`
	SpeciesCount int    `json:"speciesCount"`
	Default      bool   `json:"default"`
	Installed    bool   `json:"installed"`
	// BuiltIn marks the embedded baseline variant (the built-in BirdNET v2.4 model).
	// It carries no downloadable files; the gallery labels it and shows no size.
	BuiltIn           bool  `json:"builtIn,omitempty"`
	SizeBytes         int64 `json:"sizeBytes"`
	HeadlineLatencyMs int   `json:"headlineLatencyMs,omitempty"`
	// Compatible reports whether this variant can run on the host. It defaults to
	// true (no hardware evaluation performed) so a client that did not receive
	// recommendations does not render every variant as incompatible.
	Compatible bool `json:"compatible"`
	// Recommended marks the single variant the gallery preselects for this host.
	Recommended bool `json:"recommended"`
	// Reasons explain the recommendation score; Blockers explain incompatibility.
	// Both are structured codes the frontend localizes, omitted when the request
	// is not eligible for hardware recommendations.
	Reasons  []VariantReasonResponse `json:"reasons,omitempty"`
	Blockers []VariantReasonResponse `json:"blockers,omitempty"`
	// HardwareClass is a coarse, localizable token naming the hardware this variant
	// targets, for the gallery's plain-language chip (never raw precision like
	// "fp16"). One of: "gpuNvidia", "gpuIntel", "amd64Cpu", "arm64Cpu", "armCpu",
	// "cpu", "builtIn". The frontend maps it to analysis.gallery.hardware.<token>.
	// The CPU tokens are made architecture-explicit from the host arch when the
	// request is eligible for recommendations; otherwise the intrinsic "cpu"/"gpu"
	// class (from the variant's own recommended backends) is emitted.
	HardwareClass string `json:"hardwareClass,omitempty"`
}

// VariantReasonResponse is a structured, localizable reason for a variant's
// compatibility or recommendation. Code is an i18n key stem (never an English
// sentence); Args carries the values the frontend interpolates into the string.
type VariantReasonResponse struct {
	Code string            `json:"code"`
	Args map[string]string `json:"args,omitempty"`
}

// installModelRequest is the optional JSON body of POST /models/install/:id. An
// absent body or empty variantId installs (or switches to) the default variant.
type installModelRequest struct {
	VariantID string `json:"variantId"`
}

// ListModels returns classifier models that are enabled in the configuration.
func (c *Handler) ListModels(ctx echo.Context) error {
	// Read from the live settings (atomic pointer) so that models added
	// at runtime (via gallery install) are immediately visible.
	settings := conf.GetSettings()

	// Build a set of enabled model config IDs for fast lookup.
	enabled := make(map[string]bool, len(settings.Models.Enabled))
	for _, id := range settings.Models.Enabled {
		enabled[strings.ToLower(id)] = true
	}

	models := make([]ModelListItem, 0, len(enabled))

	// Snapshot the active catalog once (honors a user-edited model-catalog.json).
	catalog := classifier.ActiveCatalog()
	for id := range classifier.ModelRegistry {
		info := classifier.ModelRegistry[id]
		for _, alias := range info.ConfigAliases {
			if enabled[strings.ToLower(alias)] {
				// Determine category from catalog entry (if any), default to "bird".
				category := "bird"
				for j := range catalog {
					if catalog[j].RegistryID == id {
						category = catalog[j].Category
						break
					}
				}

				models = append(models, ModelListItem{
					ID:                    alias,
					Name:                  info.DisplayName(),
					Category:              category,
					MinSampleRate:         info.Spec.MinRawSampleRate,
					RecommendedSampleRate: info.Spec.RecommendedSampleRate,
				})
				break // one entry per model
			}
		}
	}

	// Sort by ID for stable output.
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	return ctx.JSON(http.StatusOK, models)
}

// incompatibleReasonONNXUnavailable is the structured, localizable code set on
// an entry that needs ONNX Runtime when the host has none. It replaces the raw
// ORT error string (which the i18n'd gallery could not translate) and renders
// through the same reasonKey path as the variant reasons, with a fallback to the
// code itself. It is an entry-level signal (gated on RequiresONNX here, not a
// per-variant blocker), so it lives in this package rather than the recommender's
// variant vocabulary.
const incompatibleReasonONNXUnavailable = "backend.onnx_unavailable"

// GetModelCatalog returns the embedded model catalog enriched with install
// status and compatibility information.
func (c *Handler) GetModelCatalog(ctx echo.Context) error {
	visible := classifier.VisibleCatalog()
	catalog := make([]CatalogEntryResponse, 0, len(visible))

	// Check ORT availability once, reuse for all entries that require ONNX.
	ortStatus := inference.CheckORTAvailability(c.CurrentSettings().BirdNET.ONNXRuntimePath)

	// Hardware recommendations are gated to authenticated (or auth-not-required)
	// requests, so an anonymous caller on an auth-enabled instance cannot read the
	// host's hardware profile from this public endpoint. When enrichment is off,
	// recsByVariant/recommendedVariant stay nil and every variant reports the
	// neutral Compatible=true with no reasons.
	var recsByVariant map[string]map[string]recommend.Recommendation
	var recommendedVariant map[string]string
	// hostArch stays "" for an ineligible request, so the hardware-class tokens
	// degrade to the architecture-neutral "cpu"/"gpu" class rather than leaking the
	// host architecture on the public endpoint.
	hostArch := ""
	if c.recommendationsAllowed(ctx) {
		recsByVariant, recommendedVariant, hostArch = c.rankCatalog(visible, ortStatus)
	}

	for i := range visible {
		entry := &visible[i]

		// Compute total size from all files.
		var totalSize int64
		for _, f := range entry.Files {
			totalSize += f.SizeBytes
		}

		// Check install status via ModelManager, capturing which variant is on disk.
		installed := false
		installedVariantID := ""
		if c.ModelManager != nil {
			if vid, ok := c.ModelManager.InstalledVariantID(entry.ID); ok {
				installed = true
				installedVariantID = vid
			}
		}

		// Models requiring ONNX Runtime are incompatible when ORT is absent.
		compatible := true
		incompatibleReason := ""
		if entry.RequiresONNX && !ortStatus.Available {
			compatible = false
			incompatibleReason = incompatibleReasonONNXUnavailable
		}

		catalog = append(catalog, CatalogEntryResponse{
			ID:                   entry.ID,
			Name:                 entry.Name,
			Description:          entry.Description,
			Author:               entry.Author,
			License:              entry.License,
			CommercialUse:        entry.CommercialUse,
			Category:             entry.Category,
			Region:               entry.Region,
			SpeciesCount:         entry.SpeciesCount,
			Version:              entry.Version,
			Channel:              channelOrDefault(entry.Channel),
			BuildLabel:           entry.BuildLabel,
			UpstreamURL:          entry.UpstreamURL,
			Installed:            installed,
			Compatible:           compatible,
			IncompatibleReason:   incompatibleReason,
			TotalSizeBytes:       totalSize,
			HasGeomodel:          classifier.HasGeomodelFiles(entry),
			Permanent:            classifier.IsPermanentEntry(entry),
			InstalledVariantID:   installedVariantID,
			RecommendedVariantID: recommendedVariant[entry.ID],
			Variants:             buildVariantResponses(entry, installed, installedVariantID, recsByVariant[entry.ID], hostArch),
		})
	}

	// Recommended-first ordering for the Available tab: entries with a compatible
	// hardware recommendation sort ahead of entries with none, catalog order
	// preserved within each group (stable sort). A flat entry (no variants) is
	// never ranked by the recommender, so it counts as recommendable to keep it
	// from sinking below variant-bearing entries; the sort only demotes a
	// variant-bearing entry whose every variant is incompatible with this host.
	// Applied only when recommendations were computed (an eligible request); an
	// ineligible caller keeps the stable default catalog order.
	if recommendedVariant != nil {
		sort.SliceStable(catalog, func(i, j int) bool {
			ri := recommendedVariant[catalog[i].ID] != "" || len(catalog[i].Variants) == 0
			rj := recommendedVariant[catalog[j].ID] != "" || len(catalog[j].Variants) == 0
			return ri && !rj
		})
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"catalog": catalog,
	})
}

// buildVariantResponses maps a catalog entry's variants to their API form,
// joining in the per-variant recommendations when recs is non-nil (recs is nil
// when the request is not eligible for hardware recommendations, and is keyed by
// variant ID). It returns nil for a flat (single-variant) entry so the field is
// omitted, and hides a Legacy (superseded) variant unless it is the one
// currently installed, so the gallery never offers a fresh install of a
// deprecated build.
func buildVariantResponses(entry *classifier.CatalogEntry, installed bool, installedVariantID string, recs map[string]recommend.Recommendation, hostArch string) []CatalogVariantResponse {
	if len(entry.Variants) == 0 {
		return nil
	}
	variants := make([]CatalogVariantResponse, 0, len(entry.Variants))
	for j := range entry.Variants {
		v := &entry.Variants[j]
		isInstalledVariant := installed && installedVariantID == v.ID
		if v.Legacy && !isInstalledVariant {
			continue
		}

		var sizeBytes int64
		for _, f := range v.Files {
			sizeBytes += f.SizeBytes
		}

		// Headline latency: the smallest positive measured latency across this
		// variant's benchmarks, or 0 when none were measured.
		headlineLatencyMs := 0
		for _, b := range v.Benchmarks {
			if b.LatencyMs > 0 && (headlineLatencyMs == 0 || b.LatencyMs < headlineLatencyMs) {
				headlineLatencyMs = b.LatencyMs
			}
		}

		resp := CatalogVariantResponse{
			ID:                v.ID,
			Region:            v.Region,
			Precision:         v.Precision,
			SpeciesCount:      v.SpeciesCount,
			Default:           v.Default,
			Installed:         isInstalledVariant,
			BuiltIn:           v.BuiltIn,
			SizeBytes:         sizeBytes,
			HeadlineLatencyMs: headlineLatencyMs,
			// Neutral default: without a recommendation the variant is not claimed
			// incompatible. Overwritten below when recommendations are present.
			Compatible: true,
		}
		rec, hasRec := recs[v.ID]
		if hasRec {
			resp.Compatible = rec.Compatible
			resp.Recommended = rec.Recommended
			resp.Reasons = toReasonResponses(rec.Reasons)
			resp.Blockers = toReasonResponses(rec.Blockers)
		}
		resp.HardwareClass = variantHardwareClass(v, &rec, hasRec, hostArch)
		variants = append(variants, resp)
	}
	return variants
}

// Hardware-class tokens emitted on CatalogVariantResponse.HardwareClass. The
// frontend maps each to a localized chip via analysis.gallery.hardware.<token>,
// so the vocabulary stays locale-owned client-side while the classification (which
// needs the live host arch and the chosen backend) is authoritative here.
const (
	hwClassBuiltIn   = "builtIn"
	hwClassGPUNvidia = "gpuNvidia"
	hwClassGPUIntel  = "gpuIntel"
	hwClassAMD64CPU  = "amd64Cpu"
	hwClassARM64CPU  = "arm64Cpu"
	hwClassARMCPU    = "armCpu"
	hwClassCPU       = "cpu"
)

// Backend tokens used when classifying a variant's hardware target. The OpenVINO
// and ONNX tokens are sourced from their canonical owner (hwprofile) so a rename
// there is a compile error here rather than a silent misclassification; cuda and
// tensorrt have no exported source (recommend's copies are unexported), so they
// mirror those tokens as literals.
const (
	backendCUDA        = "cuda"
	backendTensorRT    = "tensorrt"
	backendOpenVINOGPU = hwprofile.CapOpenVINOGPU
	backendONNXCPU     = hwprofile.CapONNXRuntimeCPU
	backendOpenVINOCPU = hwprofile.CapOpenVINOCPU
)

// channelOrDefault normalizes an empty release channel to the stable channel so
// the API always reports a concrete channel and the frontend never has to guess
// what an absent channel means.
func channelOrDefault(channel string) string {
	if channel == "" {
		return classifier.ChannelStable
	}
	return channel
}

// variantHardwareClass derives the coarse hardware-target token for a variant's
// gallery chip (never raw precision). It prefers the backend the recommender chose
// for THIS host, so the chip matches how the variant will actually run for the
// viewer, and falls back to the variant's own recommended backends when no host
// recommendation is present (an ineligible request, a blocked variant with no
// reasons, or a flat entry). GPU builds report the discrete-vs-Intel split; a CPU
// build is made architecture-explicit (amd64Cpu/arm64Cpu) from an ARM-only variant
// requirement or, failing that, the host arch, and stays the generic "cpu" when the
// host arch is unknown. The built-in baseline wins over everything.
func variantHardwareClass(v *classifier.CatalogVariant, rec *recommend.Recommendation, hasRec bool, hostArch string) string {
	if v.BuiltIn {
		return hwClassBuiltIn
	}
	backend := ""
	if hasRec {
		backend = recommendedBackendToken(rec.Reasons)
	}
	if backend == "" {
		backend = intrinsicGPUBackend(v.Backends)
	}
	switch backend {
	case backendCUDA, backendTensorRT:
		return hwClassGPUNvidia
	case backendOpenVINOGPU:
		return hwClassGPUIntel
	}
	// A CPU-class build. Make the arch explicit: an ARM-restricted variant is
	// labelled by the ARM width it targets (arm64Cpu vs armCpu), regardless of host;
	// otherwise the arch-neutral CPU build is labelled for the viewer's host arch
	// (accurate because the gallery is host-specific), or stays the generic "cpu"
	// when the host arch is unknown (an ineligible request).
	if armClass := variantARMClass(v); armClass != "" {
		return armClass
	}
	switch hostArch {
	case archARM64:
		return hwClassARM64CPU
	case archARM:
		return hwClassARMCPU
	case archAMD64:
		return hwClassAMD64CPU
	}
	return hwClassCPU
}

// Host architecture identifiers (runtime.GOARCH), mirrored from hwprofile so the
// CPU-class token can be made architecture-explicit.
const (
	archAMD64 = "amd64"
	archARM64 = "arm64"
	archARM   = "arm"
)

// recommendedBackendToken extracts the chosen backend token from a variant's
// recommendation reasons: the explicit backend.recommended reason, else the first
// reason carrying a backend arg. Mirrors the frontend chosenBackendToken fallback.
func recommendedBackendToken(reasons []recommend.Reason) string {
	fallback := ""
	for i := range reasons {
		b := reasons[i].Args[recommend.ReasonArgBackend]
		if b == "" {
			continue
		}
		if reasons[i].Code == recommend.ReasonBackendRecommended {
			return b
		}
		if fallback == "" {
			fallback = b
		}
	}
	return fallback
}

// intrinsicGPUBackend returns the variant's recommended GPU backend token when the
// variant is a GPU-oriented build (recommended on a GPU backend and NOT recommended
// on any CPU backend), else "" so it classifies as a CPU build. A build recommended
// on both CPU and GPU (e.g. a general fp32) is treated as a CPU build: the CPU is
// its plain-language target and the GPU-optimized build is offered as a separate
// variant.
func intrinsicGPUBackend(backends map[string]classifier.BackendSupport) string {
	if backends[backendONNXCPU].Recommended || backends[backendOpenVINOCPU].Recommended {
		return ""
	}
	switch {
	case backends[backendCUDA].Recommended || backends[backendTensorRT].Recommended:
		return backendCUDA
	case backends[backendOpenVINOGPU].Recommended:
		return backendOpenVINOGPU
	}
	return ""
}

// variantARMClass returns the ARM CPU class a variant is restricted to, or "" when
// it is not ARM-restricted. It reads the arch requirement: a 64-bit ARM token (the
// aarch64 family "aarch64"/"aarch64-a76", or "arm64") yields arm64Cpu; a 32-bit ARM
// token ("arm"/"armv7l"/"armhf") yields armCpu, so a 32-bit host is never mislabelled
// as ARM64. For older entries that predate arch requirements it falls back to an
// "arm" token in the variant id (the catalog's arm builds are 64-bit, e.g.
// "int8-arm"), yielding arm64Cpu.
func variantARMClass(v *classifier.CatalogVariant) string {
	for _, a := range v.Requirements.Arch {
		lower := strings.ToLower(a)
		if strings.HasPrefix(lower, "aarch") || lower == archARM64 {
			return hwClassARM64CPU
		}
		if strings.Contains(lower, "arm") {
			return hwClassARMCPU
		}
	}
	if strings.Contains(strings.ToLower(v.ID), "arm") {
		return hwClassARM64CPU
	}
	return ""
}

// recommendationsAllowed reports whether this request may receive the
// hardware-derived recommendation fields. Auth-not-required (a home LAN with
// auth disabled, or a subnet-bypass client) and authenticated requests both
// qualify; an anonymous request on an auth-enabled instance does not. A nil
// authService (tests, or WithAuthService not injected) allows enrichment,
// because there is then no auth boundary whose leak we are guarding.
func (c *Handler) recommendationsAllowed(ctx echo.Context) bool {
	return c.authService == nil || c.authService.IsAuthenticated(ctx)
}

// rankCatalog computes the per-host variant recommendations for the visible
// catalog, indexed by catalog ID then variant ID, plus the recommended variant
// per entry.
func (c *Handler) rankCatalog(entries []classifier.CatalogEntry, ort inference.ORTStatus) (byVariant map[string]map[string]recommend.Recommendation, recommended map[string]string, hostArch string) {
	profileFn := c.hardwareProfile
	if profileFn == nil {
		profileFn = defaultHardwareProfile
	}
	profile := profileFn(ort)
	hostArch = profile.Arch
	recs := recommend.Rank(&recommend.Input{
		Capabilities:   profile.Capabilities(),
		TotalRAMBytes:  profile.TotalRAMBytes,
		DeviceClass:    recommend.DeviceClass(profile.Board.Tier, profile.Arch),
		ResolvedRegion: c.resolveRecommendRegion(),
		Entries:        entries,
	})

	byVariant = make(map[string]map[string]recommend.Recommendation, len(entries))
	recommended = make(map[string]string)
	for i := range recs {
		r := &recs[i]
		m := byVariant[r.CatalogID]
		if m == nil {
			m = make(map[string]recommend.Recommendation)
			byVariant[r.CatalogID] = m
		}
		m[r.VariantID] = *r
		if r.Recommended {
			recommended[r.CatalogID] = r.VariantID
		}
	}
	return byVariant, recommended, hostArch
}

// defaultHardwareProfile resolves the live host profile from the already-probed
// ONNX Runtime status plus a per-request OpenVINO device probe, mirroring the
// inference-status endpoint (internal/api/v2/system/inference_status.go). The ORT
// status is passed in (probed once per request by GetModelCatalog) rather than
// re-probed here, and the OpenVINO device list feeds GPU capability derivation.
// It is the production value of the hardwareProfile seam.
func defaultHardwareProfile(ort inference.ORTStatus) hwprofile.Profile {
	ov := inference.CheckOpenVINOAvailability()
	var devices []string
	if ov.Supported {
		for _, d := range []string{inference.OVDeviceCPU, inference.OVDeviceGPU} {
			if inference.OpenVINOHasDevice(d) {
				devices = append(devices, d)
			}
		}
	}
	return hwprofile.Hardware().WithBackends(hwprofile.Backends{
		TFLite:   hwprofile.BackendStatus{Available: hwprofile.TFLiteLinked()},
		ONNX:     hwprofile.BackendStatus{Available: ort.Available, Initialized: ort.Initialized, Version: ort.Version},
		OpenVINO: hwprofile.OpenVINOStatus{Supported: ov.Supported, Active: ov.Active, Devices: devices},
	})
}

// toReasonResponses maps recommender reasons to their API form.
func toReasonResponses(reasons []recommend.Reason) []VariantReasonResponse {
	if len(reasons) == 0 {
		return nil
	}
	out := make([]VariantReasonResponse, len(reasons))
	for i := range reasons {
		out[i] = VariantReasonResponse{Code: reasons[i].Code, Args: reasons[i].Args}
	}
	return out
}

// GetInstalledModels returns all models that have been downloaded and installed.
func (c *Handler) GetInstalledModels(ctx echo.Context) error {
	if c.ModelManager == nil {
		return ctx.JSON(http.StatusOK, []classifier.InstalledModel{})
	}

	return ctx.JSON(http.StatusOK, c.ModelManager.ListInstalled())
}

// InstallModel starts an asynchronous model download and installation.
// It returns 202 Accepted immediately while the download runs in the background.
func (c *Handler) InstallModel(ctx echo.Context) error {
	catalogID := ctx.Param("id")
	if catalogID == "" {
		return c.HandleError(ctx, nil, "catalog ID is required", http.StatusBadRequest)
	}

	entry, ok := classifier.GetCatalogEntry(catalogID)
	if !ok {
		return c.HandleError(ctx, nil, "unknown catalog ID: "+catalogID, http.StatusNotFound)
	}

	// Hidden entries are foundation-only: excluded from the gallery and not meant to
	// be installed by ID. The permanent BirdNET v2.4 entry is intentionally NOT
	// hidden: it is always installed, and an install request against it is a
	// within-model variant swap routed to InstallOrReplace -> replacePrimaryVariant.
	if entry.Hidden {
		return c.HandleError(ctx, nil, "catalog entry "+catalogID+" is not available for installation", http.StatusNotFound)
	}

	if c.ModelManager == nil {
		return c.HandleError(ctx, nil, "model manager is not available", http.StatusServiceUnavailable)
	}

	// Parse the optional variant selection from the request body. The body is
	// optional: an absent or empty body installs the default variant, preserving
	// the pre-variant call shape, so an empty-body io.EOF is tolerated. Decoding the
	// body directly (rather than ctx.Bind, which 400s an empty body under a JSON
	// content-type, and rather than gating on ContentLength, which is -1 for chunked
	// requests) keeps that contract robust while still binding a real chunked body.
	// Malformed JSON is a 400 so the client sees the error synchronously rather than
	// as a silent async failure over the progress stream. The body reader is bounded
	// by the server's body-limit middleware.
	var req installModelRequest
	if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return c.HandleError(ctx, err, "invalid request body", http.StatusBadRequest)
	}
	if !classifier.VariantSelectable(&entry, req.VariantID) {
		return c.HandleError(ctx, nil, "unknown variant "+req.VariantID+" for model "+catalogID, http.StatusBadRequest)
	}

	// Reject installation of ONNX-dependent models when ORT is unavailable. The gate
	// is variant-scoped: an entry may be ORT-free overall (e.g. BirdNET v2.4, whose
	// BuiltIn baseline runs on the embedded TFLite model) yet expose ONNX-only
	// variants (the DFT-truncated builds). VariantNeedsONNX refines the entry-level
	// flag so swapping to the embedded baseline is never blocked, while selecting an
	// ONNX build still requires the runtime.
	if entry.RequiresONNX || classifier.VariantNeedsONNX(&entry, req.VariantID) {
		ortStatus := inference.CheckORTAvailability(c.CurrentSettings().BirdNET.ONNXRuntimePath)
		if !ortStatus.Available {
			return c.HandleError(ctx, nil,
				"model requires ONNX Runtime "+inference.ORTRequiredVersion()+": "+ortStatus.Error,
				http.StatusConflict)
		}
	}

	// Start async install in a background goroutine.
	progressChan := make(chan classifier.DownloadState, 16)
	c.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				c.LogErrorIfEnabled("Panic during model install",
					logger.String("catalog_id", catalogID),
					logger.Any("panic", r),
				)
			}
		}()
		if err := c.ModelManager.InstallOrReplace(c.Context(), &entry, req.VariantID, "", progressChan); err != nil {
			c.LogErrorIfEnabled("Model install failed",
				logger.String("catalog_id", catalogID),
				logger.String("variant_id", req.VariantID),
				logger.Error(err),
			)
		}
		close(progressChan)

		for range progressChan {
		}
	})

	return ctx.JSON(http.StatusAccepted, map[string]string{
		"catalogId": catalogID,
		"variantId": req.VariantID,
		"status":    classifier.StatusDownloading,
	})
}

// ReinstallModel re-downloads missing or corrupt files for an installed model.
// Files that pass SHA256 validation are skipped. It returns 202 Accepted
// immediately while the re-download runs in the background.
func (c *Handler) ReinstallModel(ctx echo.Context) error {
	catalogID := ctx.Param("id")
	if catalogID == "" {
		return c.HandleError(ctx, nil, "catalog ID is required", http.StatusBadRequest)
	}

	entry, ok := classifier.GetCatalogEntry(catalogID)
	if !ok {
		return c.HandleError(ctx, nil, "unknown catalog ID: "+catalogID, http.StatusNotFound)
	}

	// Hidden entries are foundation-only and not installable by ID (see InstallModel).
	if entry.Hidden {
		return c.HandleError(ctx, nil, "catalog entry "+catalogID+" is not available for installation", http.StatusNotFound)
	}

	// The permanent BirdNET v2.4 entry is now visible (so its variant can be
	// swapped), but reinstall has no meaning for it: the BuiltIn baseline has no
	// downloadable files, and a DFT variant is (re)acquired by swapping to it via
	// InstallOrReplace. Refuse reinstall explicitly rather than let it fall through.
	if classifier.IsPermanentEntry(&entry) {
		return c.HandleError(ctx, nil, "the built-in "+entry.Name+" model cannot be reinstalled", http.StatusConflict)
	}

	if c.ModelManager == nil {
		return c.HandleError(ctx, nil, "model manager is not available", http.StatusServiceUnavailable)
	}

	if !c.ModelManager.IsInstalled(catalogID) {
		return c.HandleError(ctx, nil, "model "+catalogID+" is not installed", http.StatusBadRequest)
	}

	// Reject reinstall of ONNX-dependent models when ORT is unavailable.
	if entry.RequiresONNX {
		ortStatus := inference.CheckORTAvailability(c.CurrentSettings().BirdNET.ONNXRuntimePath)
		if !ortStatus.Available {
			return c.HandleError(ctx, nil,
				"model requires ONNX Runtime "+inference.ORTRequiredVersion()+": "+ortStatus.Error,
				http.StatusConflict)
		}
	}

	// Start async reinstall in a background goroutine.
	progressChan := make(chan classifier.DownloadState, 16)
	c.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				c.LogErrorIfEnabled("Panic during model reinstall",
					logger.String("catalog_id", catalogID),
					logger.Any("panic", r),
				)
			}
		}()
		if err := c.ModelManager.Reinstall(c.Context(), &entry, "", progressChan); err != nil {
			c.LogErrorIfEnabled("Model reinstall failed",
				logger.String("catalog_id", catalogID),
				logger.Error(err),
			)
		}
		close(progressChan)

		for range progressChan {
		}
	})

	return ctx.JSON(http.StatusAccepted, map[string]string{
		"catalogId": catalogID,
		"status":    classifier.StatusDownloading,
	})
}

// UninstallModel removes a downloaded model from disk.
func (c *Handler) UninstallModel(ctx echo.Context) error {
	catalogID := ctx.Param("id")
	if catalogID == "" {
		return c.HandleError(ctx, nil, "catalog ID is required", http.StatusBadRequest)
	}

	if c.ModelManager == nil {
		return c.HandleError(ctx, nil, "model manager is not available", http.StatusServiceUnavailable)
	}

	if err := c.ModelManager.Uninstall(catalogID); err != nil {
		return c.HandleError(ctx, err, "failed to uninstall model", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"catalogId": catalogID,
		"status":    classifier.StatusRemoved,
	})
}

// StreamInstallProgress streams model download progress as Server-Sent Events.
// The stream closes automatically when the download completes or fails.
func (c *Handler) StreamInstallProgress(ctx echo.Context) error {
	catalogID := ctx.Param("id")
	if catalogID == "" {
		return c.HandleError(ctx, nil, "catalog ID is required", http.StatusBadRequest)
	}

	if c.ModelManager == nil {
		return c.HandleError(ctx, nil, "model manager is not available", http.StatusServiceUnavailable)
	}

	// Set SSE headers.
	apicore.SetSSEHeaders(ctx)

	// Flush helper for the response writer.
	flusher, ok := ctx.Response().Writer.(http.Flusher)
	if !ok {
		return c.HandleError(ctx, nil, "streaming not supported", http.StatusInternalServerError)
	}

	ticker := time.NewTicker(apicore.SSEHeartbeatInterval)
	defer ticker.Stop()

	reqCtx := ctx.Request().Context()

	// Track how long we see nil download state without the model being
	// installed. If this exceeds a threshold, the install likely failed
	// and the state was already cleaned up before we connected.
	const maxNoStateIterations = 30000 // ~5 min at 10ms sleep
	noStateCount := 0

	for {
		select {
		case <-reqCtx.Done():
			// Client disconnected.
			return nil

		case <-ticker.C:
			// Send heartbeat to keep the connection alive.
			heartbeat := map[string]any{
				"timestamp": time.Now().Unix(),
			}
			if err := writeSSEEvent(ctx, "heartbeat", heartbeat); err != nil {
				return nil
			}
			flusher.Flush()

		default:
			state := c.ModelManager.GetDownloadState(catalogID)
			if state == nil {
				// No active download. Check if the model is already installed,
				// which means the download completed before we connected.
				if c.ModelManager.IsInstalled(catalogID) {
					completeState := classifier.DownloadState{
						CatalogID: catalogID,
						Status:    classifier.StatusComplete,
					}
					_ = writeSSEEvent(ctx, "progress", completeState)
					flusher.Flush()
					return nil
				}

				noStateCount++
				if noStateCount > maxNoStateIterations {
					// Timeout: no download state observed for too long.
					failedState := classifier.DownloadState{
						CatalogID: catalogID,
						Status:    classifier.StatusFailed,
						Error:     "install timed out or failed before progress could be tracked",
					}
					_ = writeSSEEvent(ctx, "progress", failedState)
					flusher.Flush()
					return nil
				}

				// No download and not installed: nothing to report yet.
				// Wait briefly before re-checking.
				time.Sleep(apicore.SSEEventLoopSleep)
				continue
			}

			// Reset counter when we have valid state.
			noStateCount = 0

			// Send current progress.
			if err := writeSSEEvent(ctx, "progress", state); err != nil {
				return nil
			}
			flusher.Flush()

			// Terminal states end the stream.
			if state.Status == classifier.StatusComplete || state.Status == classifier.StatusFailed {
				return nil
			}

			// Small sleep to avoid busy-waiting.
			time.Sleep(apicore.SSEEventLoopSleep)
		}
	}
}

// writeSSEEvent writes a single SSE event to the response.
func writeSSEEvent(ctx echo.Context, event string, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}

	message := fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(jsonData))
	if _, err := ctx.Response().Write([]byte(message)); err != nil {
		return fmt.Errorf("failed to write SSE message: %w", err)
	}

	return nil
}
