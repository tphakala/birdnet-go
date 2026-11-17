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
	"slices"
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
	// It receives the caller's already-probed ONNX Runtime status (a catalog or
	// install request, or the optimize notice evaluation) so the default probe
	// does not re-check ORT.
	hardwareProfile func(ort inference.ORTStatus) hwprofile.Profile
	// ensureOVProbe runs the out-of-process OpenVINO device probe before a
	// production-profile ranking that may not wait on a request (see
	// ensureHostProbed). nil means inference.EnsureOpenVINOProbe; tests inject a
	// counter.
	ensureOVProbe func()
	// ortProbe reports the ONNX Runtime status for the configured library path.
	// nil means inference.CheckORTAvailability; tests inject a fixed status so
	// the install gate and the recommender do not depend on the host runtime.
	ortProbe func(configuredPath string) inference.ORTStatus
	// notices is the notification sink for the optimize bell notice. nil means
	// the process-wide notification service; tests inject a fake.
	notices noticeService
	// optimize latches the optimize bell notice and its debounce timer
	// (optimize_notice.go). Handler is only ever used by pointer, so the mutexes
	// inside are never copied.
	optimize optimizeNotice
	// superseded latches the superseded-model bell notice (superseded_notice.go),
	// evaluated alongside the optimize notice.
	superseded supersededNotice
	// modelRuntime reports the live backend and precision of each loaded model,
	// keyed by registry ID. It is an injectable seam: nil means the live default
	// (liveModelRuntime, read from the model manager and the orchestrator), and
	// tests inject a fixed map. A per-Handler field so tests stay parallel-safe.
	modelRuntime func() map[string]listedModelRuntime
	// loadErrors reports the last load error text per registry ID for enabled
	// models that failed to load. Injectable like modelRuntime: nil means the live
	// orchestrator's LoadErrors, and tests inject a fixed map.
	loadErrors func() map[string]string
	// announceListChange tells connected clients that the model list changed. nil
	// means a topology broadcast on the metrics store, and tests inject a counter.
	announceListChange func()
}

// Model load states reported in ModelListItem.State.
const (
	// ModelStateLoaded means the model is loaded and can run.
	ModelStateLoaded = "loaded"
	// ModelStateFailed means the model is enabled, not loaded, and its last load
	// attempt failed.
	ModelStateFailed = "failed"
	// ModelStatePending means the model is enabled, not loaded, and no load error
	// is on record, so a load is still expected.
	ModelStatePending = "pending"
)

// listedModelRuntime is the live execution backend and runtime precision of one
// loaded model, as the list endpoint reports them.
type listedModelRuntime struct {
	backend   string
	precision string
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
	ID   string `json:"id"`   // Config alias (e.g., "birdnet", "perch_v2")
	Name string `json:"name"` // Display name without a backend suffix (e.g., "BirdNET v2.4")
	// RegistryID is the classifier registry ID (e.g., "BirdNET_V2.4"). It is the
	// join key against the registry-ID entries in the defaultTargets list served by
	// GET /api/v2/system/inference, letting the frontend map default targets back to
	// the config aliases this endpoint uses (model de-privilege epic, Phase 4).
	RegistryID            string `json:"registryId"`
	Category              string `json:"category"`                        // Model category (e.g., "bird", "bat")
	MinSampleRate         int    `json:"minSampleRate,omitempty"`         // Minimum required sample rate in Hz
	RecommendedSampleRate int    `json:"recommendedSampleRate,omitempty"` // Recommended sample rate in Hz
	// Backend is the live execution backend of the loaded model (for example
	// "TFLite", "ONNX" or "OpenVINO"). Omitted when the model is not loaded or the
	// request may not receive host-derived data.
	Backend string `json:"backend,omitempty"`
	// Precision is the live runtime precision of the loaded model (for example
	// "FP32" or "INT8"). Omitted under the same conditions as Backend.
	Precision string `json:"precision,omitempty"`
	// State is the model's load state: ModelStateLoaded, ModelStateFailed or
	// ModelStatePending. Unlike Backend and Precision it is not host-derived, so it
	// is reported to every caller.
	State string `json:"state"`
}

// CatalogDocumentResponse is one licence or policy document of a catalog entry's
// own repo. The repo-relative path and pinned revision are not exposed.
type CatalogDocumentResponse struct {
	// Repo is the HuggingFace repo that owns the document.
	Repo string `json:"repo"`
	// Kind is the document kind, for example "license" or "acceptable_use".
	Kind string `json:"kind"`
	// RequiresAgreement marks a document the user must agree to before installing.
	RequiresAgreement bool `json:"requiresAgreement"`
	// SHA256 identifies a hosted document; omitted for an external one.
	SHA256 string `json:"sha256,omitempty"`
	// URL is the location of an external document; omitted for a hosted one.
	URL string `json:"url,omitempty"`
}

// CatalogEntryResponse represents a model in the catalog API response.
type CatalogEntryResponse struct {
	ID string `json:"id"`
	// RegistryID is the classifier registry ID the entry loads as (e.g.
	// "BirdNET_V2.4"). It is the join key against the registryId of the items
	// served by GET /api/v2/models (enabled models only), which carry the
	// config alias that audio source model lists use. Several entries can
	// share one ID (the regional BattyBirdNET models all report "Bat").
	// Omitted for an entry that is not a classifier, such as the geomodel.
	RegistryID    string `json:"registryId,omitempty"`
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
	// developer-preview build the gallery flags as not the final release, and
	// "experimental" for a published model whose performance in BirdNET-Go is not
	// proven yet. Always populated (defaults to "stable") so the frontend never has
	// to guess.
	Channel string `json:"channel"`
	// BuildLabel is a human-facing build tag shown next to Version for a non-stable
	// channel (e.g. "preview3.1"); omitted for stable releases.
	BuildLabel string `json:"buildLabel,omitempty"`
	// SupersededBy names the visible catalog entry that replaces this one. Omitted
	// when nothing replaces it or the successor is hidden, so the gallery never
	// points at a model it does not show.
	SupersededBy *CatalogSuccessorResponse `json:"supersededBy,omitempty"`
	UpstreamURL  string                    `json:"upstreamUrl,omitempty"`
	Installed    bool                      `json:"installed"`
	Compatible   bool                      `json:"compatible"`
	// IncompatibleReason is a structured, localizable code (an i18n key stem such
	// as "backend.onnx_unavailable"), never a raw English message, so the fully
	// i18n'd gallery can translate it; a client renders it through the same reason
	// path as the variant reasons, falling back to the code. The specific
	// underlying error (e.g. which ONNX Runtime library was missing) is not in
	// this passive listing; it is returned in full when the user attempts to
	// install the model.
	IncompatibleReason string `json:"incompatibleReason,omitempty"`
	// TotalSizeBytes is the download size of the default variant: its own files plus the
	// files of its dependencies.
	TotalSizeBytes int64 `json:"totalSizeBytes"`
	// HasGeomodel reports whether the default variant brings a geomodel, inline or
	// through a dependency.
	HasGeomodel bool `json:"hasGeomodel"`
	// DependsOn lists the catalog IDs installed together with this model. It may name
	// hidden components that the catalog response does not list. Omitted when empty.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Permanent marks the built-in BirdNET v2.4 classifier: always installed, never
	// uninstallable, only its variant may be swapped. The gallery renders a built-in
	// badge instead of Remove/Reinstall for it.
	Permanent bool `json:"permanent,omitempty"`
	// InstalledVariantID is the id of the currently installed variant, or "" when
	// the model is not installed or is a flat (pre-variant) entry.
	InstalledVariantID string `json:"installedVariantId,omitempty"`
	// Documents lists the licence and policy documents of this entry's own repo.
	// It never lists the documents of files installed together with the entry (for
	// example the geomodel files that arrive with a classifier). Omitted when the
	// repo has none.
	Documents []CatalogDocumentResponse `json:"documents,omitempty"`
	// Variants lists the selectable hardware/regional variants of this model,
	// omitted for flat single-variant entries.
	Variants []CatalogVariantResponse `json:"variants,omitempty"`
	// RecommendedVariantID is the variant the gallery preselects for this host.
	// Omitted for flat entries and when the request is not eligible for hardware
	// recommendations (an unauthenticated request on an auth-enabled instance).
	RecommendedVariantID string `json:"recommendedVariantId,omitempty"`
}

// CatalogSuccessorResponse identifies the catalog entry that supersedes another.
type CatalogSuccessorResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
	BuiltIn   bool  `json:"builtIn,omitempty"`
	SizeBytes int64 `json:"sizeBytes"`
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
	// AllowIncompatible overrides the hardware-compatibility gate. When false (the
	// default), installing a variant the catalog marks incompatible with the
	// detected hardware is rejected with 409. When true, an advanced user can force
	// the install; it still proceeds but a WARN is logged so the choice is visible
	// in logs and support dumps.
	AllowIncompatible bool `json:"allowIncompatible"`
}

// liveModelRuntime reads the backend and precision of every loaded model, keyed
// by registry ID. The orchestrator's live instance wins when it reports a value;
// the loaded model's own info is the fallback. Without a model manager the
// result is empty, so no model is reported as loaded.
func (c *Handler) liveModelRuntime() map[string]listedModelRuntime {
	if c.ModelManager == nil {
		return nil
	}
	var instanceRuntime func(id string) (backend, precision string)
	if c.Processor != nil {
		if orch := c.Processor.GetBirdNET(); orch != nil {
			instanceRuntime = func(id string) (backend, precision string) {
				_, backend, precision = orch.GetModelRuntimeInfo(id)
				return backend, precision
			}
		}
	}
	return mergeModelRuntime(c.ModelManager.ModelInfos(), instanceRuntime)
}

// mergeModelRuntime builds the runtime map for the loaded models. A non-empty
// value from instanceRuntime (the live instance, which may be nil) replaces the
// one from the model's info. The precedence matches applyRuntimeBackend in the
// system package, which serves GET /api/v2/system/inference.
func mergeModelRuntime(infos []classifier.ModelInfo, instanceRuntime func(id string) (backend, precision string)) map[string]listedModelRuntime {
	runtime := make(map[string]listedModelRuntime, len(infos))
	for i := range infos {
		item := listedModelRuntime{backend: infos[i].Backend, precision: string(infos[i].Quantization)}
		if instanceRuntime != nil {
			backend, precision := instanceRuntime(infos[i].ID)
			if backend != "" {
				item.backend = backend
			}
			if precision != "" {
				item.precision = precision
			}
		}
		runtime[infos[i].ID] = item
	}
	return runtime
}

// liveLoadErrors reads the orchestrator's current load errors, keyed by registry
// ID. Without a processor or orchestrator there are none.
func (c *Handler) liveLoadErrors() map[string]string {
	if c.Processor == nil {
		return nil
	}
	orch := c.Processor.GetBirdNET()
	if orch == nil {
		return nil
	}
	return orch.LoadErrors()
}

// modelState classifies an enabled model: loaded when the runtime reported it,
// failed when its load error is on record, else pending.
func modelState(loaded bool, loadErrors map[string]string, registryID string) string {
	if loaded {
		return ModelStateLoaded
	}
	if _, failed := loadErrors[registryID]; failed {
		return ModelStateFailed
	}
	return ModelStatePending
}

// announceModelListChange tells connected clients that the model list changed.
// It covers the changes the model manager does not announce itself: an uninstall
// of a model that was not loaded, and an install whose hot load failed. It is
// also called after a successful install, reinstall or uninstall, where a model
// that loaded or unloaded is announced by the manager as well, so a client can
// receive two events and refetches once after its debounce. It only broadcasts;
// it does not schedule an audio reconfigure.
func (c *Handler) announceModelListChange() {
	if c.announceListChange != nil {
		c.announceListChange()
		return
	}
	if c.MetricsStore != nil {
		c.MetricsStore.BroadcastTopologyChanged()
	}
}

// catalogCategory returns the category of the catalog entry for a registry ID,
// defaulting to "bird" when the catalog has no entry for it.
func catalogCategory(catalog []classifier.CatalogEntry, registryID string) string {
	for i := range catalog {
		if catalog[i].RegistryID == registryID {
			return catalog[i].Category
		}
	}
	return "bird"
}

// ListModels returns classifier models that are enabled in the configuration,
// each with its load state (loaded, failed or pending). Loaded models also carry
// their live backend and precision, for requests that may receive host-derived
// data (see recommendationsAllowed).
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

	// The loaded set decides each model's state for every caller. Backend and
	// precision are host-derived and this route is public, so they follow the same
	// gate as the catalog's host data.
	readRuntime := c.modelRuntime
	if readRuntime == nil {
		readRuntime = c.liveModelRuntime
	}
	runtime := readRuntime()
	readErrors := c.loadErrors
	if readErrors == nil {
		readErrors = c.liveLoadErrors
	}
	loadErrors := readErrors()
	withRuntime := c.recommendationsAllowed(ctx)

	// Snapshot the active catalog once (honors a user-edited model-catalog.json).
	catalog := classifier.ActiveCatalog()
	for id := range classifier.ModelRegistry {
		info := classifier.ModelRegistry[id]
		for _, alias := range info.ConfigAliases {
			if enabled[strings.ToLower(alias)] {
				live, loaded := runtime[id]
				if !withRuntime {
					live = listedModelRuntime{}
				}
				models = append(models, ModelListItem{
					ID:                    alias,
					Name:                  info.Name,
					RegistryID:            id,
					Category:              catalogCategory(catalog, id),
					MinSampleRate:         info.Spec.MinRawSampleRate,
					RecommendedSampleRate: info.Spec.RecommendedSampleRate,
					Backend:               live.backend,
					Precision:             live.precision,
					State:                 modelState(loaded, loadErrors, id),
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

// removeHasDependentsKey is the i18n key of the 409 returned when an uninstall is
// refused because other installed models need the model. The frontend interpolates
// error_params.name and error_params.models into it.
const removeHasDependentsKey = "analysis.gallery.errors.removeHasDependents"

// operationInProgressKey is the i18n key of the 409 returned when an install,
// reinstall or uninstall is refused because another model operation holds the
// manager's operation slot. The frontend interpolates error_params.name (the model
// whose operation is running); error_params.operation and error_params.running
// ({id, name}) identify it.
const operationInProgressKey = "analysis.gallery.errors.operationInProgress"

// operationInProgressMsg is the message of the operation-in-progress 409.
const operationInProgressMsg = "another model operation is in progress"

// operationInProgressResponse answers the 409 for a refused model operation.
func (c *Handler) operationInProgressResponse(ctx echo.Context, err error, busy *classifier.OperationInProgressError) error {
	return c.HandleErrorWithKey(ctx, err, operationInProgressMsg, http.StatusConflict,
		operationInProgressKey, map[string]any{
			"name":      busy.Running.Name,
			"operation": string(busy.Operation),
			"running":   busy.Running,
		})
}

// beginOperationError maps a BeginOperation failure: a busy slot is the 409, anything
// else is a server error.
func (c *Handler) beginOperationError(ctx echo.Context, err error) error {
	if busy, ok := errors.AsType[*classifier.OperationInProgressError](err); ok {
		return c.operationInProgressResponse(ctx, err, busy)
	}
	return c.HandleError(ctx, err, "failed to start model operation", http.StatusInternalServerError)
}

// visibleSuccessor resolves a SupersededBy id against the visible entries and
// returns nil when the id is empty or names an entry the gallery does not show.
// Only the immediate successor is considered: a chain that passes through a
// hidden entry stops there, so hiding an entry also withdraws the notes that
// point at it.
func visibleSuccessor(visible []classifier.CatalogEntry, id string) *CatalogSuccessorResponse {
	if id == "" {
		return nil
	}
	for i := range visible {
		if visible[i].ID == id {
			return &CatalogSuccessorResponse{ID: visible[i].ID, Name: visible[i].Name}
		}
	}
	return nil
}

// GetModelCatalog returns the embedded model catalog enriched with install
// status and compatibility information. An entry's successor is reported only when
// the successor is itself visible, and its documents are those of its own repo only.
func (c *Handler) GetModelCatalog(ctx echo.Context) error {
	visible := classifier.VisibleCatalog()
	catalog := make([]CatalogEntryResponse, 0, len(visible))

	// Check ORT availability once, reuse for all entries that require ONNX.
	ortStatus := c.checkORT(c.CurrentSettings().BirdNET.ONNXRuntimePath)

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

		// Compute the default variant's total size from all files it installs,
		// including its dependencies' files.
		defaultFiles, _ := classifier.EffectiveFiles(entry, "")
		totalSize := sumFileSizes(defaultFiles)

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
			RegistryID:           entry.RegistryID,
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
			SupersededBy:         visibleSuccessor(visible, entry.SupersededBy),
			UpstreamURL:          entry.UpstreamURL,
			Installed:            installed,
			Compatible:           compatible,
			IncompatibleReason:   incompatibleReason,
			TotalSizeBytes:       totalSize,
			HasGeomodel:          classifier.ProvidesGeomodel(entry, ""),
			DependsOn:            entry.DependsOn,
			Permanent:            classifier.IsPermanentEntry(entry),
			Documents:            buildDocumentResponses(entry),
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

// buildDocumentResponses maps the documents of the entry's own repo to their API
// form, or nil when it has none so the field is omitted.
func buildDocumentResponses(entry *classifier.CatalogEntry) []CatalogDocumentResponse {
	docs := classifier.EntryDocuments(entry)
	if len(docs) == 0 {
		return nil
	}
	out := make([]CatalogDocumentResponse, 0, len(docs))
	for i := range docs {
		out = append(out, CatalogDocumentResponse{
			Repo:              docs[i].Repo,
			Kind:              docs[i].Kind,
			RequiresAgreement: docs[i].RequiresAgreement,
			SHA256:            docs[i].SHA256,
			URL:               docs[i].URL,
		})
	}
	return out
}

// buildVariantResponses maps a catalog entry's variants to their API form,
// joining in the per-variant recommendations when recs is non-nil (recs is nil
// when the request is not eligible for hardware recommendations, and is keyed by
// variant ID). It returns nil for a flat entry (no Variants) so the field is
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

		variantFiles, _ := classifier.EffectiveFiles(entry, v.ID)
		sizeBytes := sumFileSizes(variantFiles)

		resp := CatalogVariantResponse{
			ID:           v.ID,
			Region:       v.Region,
			Precision:    v.Precision,
			SpeciesCount: v.SpeciesCount,
			Default:      v.Default,
			Installed:    isInstalledVariant,
			BuiltIn:      v.BuiltIn,
			SizeBytes:    sizeBytes,
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
// per entry. The RAM floor check uses the profile's RAM upper bound, so a host
// whose cgroup limit was unreadable is still blocked when even the upper bound
// is below a variant's floor.
func (c *Handler) rankCatalog(entries []classifier.CatalogEntry, ort inference.ORTStatus) (byVariant map[string]map[string]recommend.Recommendation, recommended map[string]string, hostArch string) {
	profileFn := c.hardwareProfile
	if profileFn == nil {
		profileFn = defaultHardwareProfile
	}
	profile := profileFn(ort)
	hostArch = profile.Arch
	recs := recommend.Rank(&recommend.Input{
		Capabilities:   profile.Capabilities(),
		TotalRAMBytes:  profile.RAMUpperBound(),
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

// requestedVariantCompatibility reports the hardware compatibility of the
// requested variant of entry, reusing the same recommender and host profile
// GetModelCatalog uses so the install gate and the gallery's compatible flag stay
// in agreement. gated is false for an entry the recommender does not rank (a flat
// entry with no variants, or a variant it produced no verdict for), for which
// there is nothing to gate; callers must treat gated==false as "allow". An empty
// variantID resolves to the entry's default variant, matching install semantics.
// It does not wait for the OpenVINO device probe: this runs on the install
// request, and a probe child that hangs can take up to its timeout, longer than
// the server's write timeout. The startup optimize evaluation runs the probe a
// few seconds after boot, and allowIncompatible overrides the gate.
func (c *Handler) requestedVariantCompatibility(entry *classifier.CatalogEntry, variantID string, ort inference.ORTStatus) (compatible, gated bool, blockers []recommend.Reason, hostArch string) {
	if entry == nil || len(entry.Variants) == 0 {
		return true, false, nil, ""
	}
	resolvedID := variantID
	if resolvedID == "" {
		resolvedID = classifier.DefaultVariantID(entry)
	}
	byVariant, _, hostArch := c.rankCatalog([]classifier.CatalogEntry{*entry}, ort)
	rec, ok := byVariant[entry.ID][resolvedID]
	if !ok {
		// The recommender produced no verdict for this variant (profiling seam
		// returned nothing, or a variant the ranker skipped). Do not block on a
		// missing verdict; offering the model is safer than a spurious rejection.
		return true, false, nil, hostArch
	}
	return rec.Compatible, true, rec.Blockers, hostArch
}

// checkORT probes ONNX Runtime availability through the ortProbe seam, falling
// back to inference.CheckORTAvailability when no probe is injected.
func (c *Handler) checkORT(configuredPath string) inference.ORTStatus {
	if c.ortProbe != nil {
		return c.ortProbe(configuredPath)
	}
	return inference.CheckORTAvailability(configuredPath)
}

// ensureHostProbed runs the out-of-process OpenVINO device probe when the live
// host profile is in use, so the optimize bell notice, which persists until the
// next trigger, sees the real device list. Without it, a host that loaded the
// OpenVINO core without probing (the explicit-CPU plan path) reports no OpenVINO
// device, because inference.OpenVINOHasDevice never enumerates in-process (issue
// #4236). It blocks while a probe child runs (a cached verdict returns at once;
// a timed-out probe is retried on the next call), so it is only called off the
// request path. It is a no-op under the hardwareProfile test seam.
func (c *Handler) ensureHostProbed() {
	if c.hardwareProfile != nil {
		return
	}
	if c.ensureOVProbe != nil {
		c.ensureOVProbe()
		return
	}
	inference.EnsureOpenVINOProbe()
}

// variantOrDefault renders a variant id for messages and logs, mapping the empty
// (default) selection to a readable token instead of an empty string.
func variantOrDefault(variantID string) string {
	if variantID == "" {
		return "(default)"
	}
	return variantID
}

// hostArchOrUnknown renders a host architecture for messages, mapping an empty
// (unresolved) arch to a readable token.
func hostArchOrUnknown(arch string) string {
	if arch == "" {
		return "unknown"
	}
	return arch
}

// formatBlockers renders recommender blocker reasons as a compact, deterministic
// string for an error message or log field (e.g. "arch.unsupported[required=aarch64]").
// Args are sorted so the output is stable across the map's iteration order.
func formatBlockers(blockers []recommend.Reason) string {
	if len(blockers) == 0 {
		return "incompatible with this host"
	}
	parts := make([]string, 0, len(blockers))
	for i := range blockers {
		b := &blockers[i]
		if len(b.Args) == 0 {
			parts = append(parts, b.Code)
			continue
		}
		args := make([]string, 0, len(b.Args))
		for k, v := range b.Args {
			args = append(args, k+"="+v)
		}
		slices.Sort(args)
		parts = append(parts, b.Code+"["+strings.Join(args, ",")+"]")
	}
	return strings.Join(parts, "; ")
}

// defaultHardwareProfile resolves the live host profile from the already-probed
// ONNX Runtime status plus the OpenVINO device list, mirroring the
// inference-status endpoint (internal/api/v2/system/inference_status.go). The ORT
// status is passed in (probed once by each caller) rather than re-probed here,
// and the OpenVINO device list, answered from the out-of-process probe cache
// (inference.OpenVINOHasDevice), feeds GPU capability derivation.
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
// It reserves the manager's operation slot before answering, so a request made
// while another model operation runs gets a synchronous 409 with
// operationInProgressKey instead of a 202 for work that never starts. When the
// background install succeeds it announces the list change, whether or not the
// hot load of the model succeeded.
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
	// within-model variant swap routed to InstallOrReplace -> replaceVariant.
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

	// Probe ORT once: it gates ONNX-only variants below and also feeds the hardware
	// profile the compatibility gate consults, mirroring how GetModelCatalog probes
	// it a single time per request.
	ortStatus := c.checkORT(c.CurrentSettings().BirdNET.ONNXRuntimePath)

	// Reject installation of ONNX-dependent models when ORT is unavailable. The gate
	// is variant-scoped: an entry may be ORT-free overall (e.g. BirdNET v2.4, whose
	// BuiltIn baseline runs on the embedded TFLite model) yet expose ONNX-only
	// variants (the DFT-truncated builds). VariantNeedsONNX refines the entry-level
	// flag so swapping to the embedded baseline is never blocked, while selecting an
	// ONNX build still requires the runtime.
	if entry.RequiresONNX || classifier.VariantNeedsONNX(&entry, req.VariantID) {
		if !ortStatus.Available {
			return c.HandleError(ctx, nil,
				"model requires ONNX Runtime "+inference.ORTRequiredVersion()+": "+ortStatus.Error,
				http.StatusConflict)
		}
	}

	// Hardware-compatibility gate. A variant the catalog marks incompatible with the
	// detected hardware (an ARM-only int8 build on an x86 host, a variant needing
	// more RAM than the host has) is rejected with 409 unless the request explicitly
	// opts in via allowIncompatible. Reusing the same recommender GetModelCatalog
	// uses keeps this gate and the gallery's compatible flag in agreement, and closes
	// the hole where a direct API call could install an incompatible variant and, on
	// switch, delete the working compatible one with no warning anywhere. An explicit
	// override still installs, but logs a WARN so the choice is visible in support dumps.
	compatible, gated, blockers, hostArch := c.requestedVariantCompatibility(&entry, req.VariantID, ortStatus)
	if gated && !compatible {
		if !req.AllowIncompatible {
			return c.HandleError(ctx, nil,
				fmt.Sprintf("variant %s of model %s is not compatible with the detected hardware (%s): %s; pass allowIncompatible to override",
					variantOrDefault(req.VariantID), catalogID, hostArchOrUnknown(hostArch), formatBlockers(blockers)),
				http.StatusConflict)
		}
		c.LogWarnIfEnabled("Installing model variant incompatible with detected hardware",
			logger.String("catalog_id", catalogID),
			logger.String("variant_id", variantOrDefault(req.VariantID)),
			logger.String("host_arch", hostArchOrUnknown(hostArch)),
			logger.String("blockers", formatBlockers(blockers)),
			logger.String("operation", "model_install_incompatible_override"))
	}

	// Reserve the operation slot synchronously, so a refusal reaches the client as a
	// 409. The lease is handed to the goroutine below, which releases it; nothing
	// between here and c.Go can return.
	lease, err := c.ModelManager.BeginOperation(classifier.OperationInstall, catalogID)
	if err != nil {
		return c.beginOperationError(ctx, err)
	}

	// Start async install in a background goroutine.
	progressChan := make(chan classifier.DownloadState, 16)
	c.Go(func() {
		defer lease.Release()
		// Re-evaluate the optimize notice whatever the outcome, panic included: an
		// install whose hot-load fails still records the install without a
		// topology event, and a failed swap may roll back to a different installed
		// variant. Deferred first, so it runs after the recover below.
		defer c.ScheduleOptimizeNoticeSync()
		defer func() {
			if r := recover(); r != nil {
				c.LogErrorIfEnabled("Panic during model install",
					logger.String("catalog_id", catalogID),
					logger.Any("panic", r),
				)
			}
		}()
		if err := lease.InstallOrReplace(c.Context(), &entry, req.VariantID, "", progressChan); err != nil {
			c.LogErrorIfEnabled("Model install failed",
				logger.String("catalog_id", catalogID),
				logger.String("variant_id", req.VariantID),
				logger.Error(err),
			)
		} else {
			c.announceModelListChange()
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
// immediately while the re-download runs in the background. Like InstallModel it
// answers a synchronous 409 with operationInProgressKey while another model
// operation runs, and announces the list change when the reinstall succeeds.
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

	// Reserve the operation slot synchronously (see InstallModel), before the
	// installed check, so an uninstall cannot complete between the check and the
	// 202. A validation failure below releases it; the goroutine owns it after.
	lease, err := c.ModelManager.BeginOperation(classifier.OperationReinstall, catalogID)
	if err != nil {
		return c.beginOperationError(ctx, err)
	}
	handedOff := false
	defer func() {
		if !handedOff {
			lease.Release()
		}
	}()

	if !c.ModelManager.IsInstalled(catalogID) {
		return c.HandleError(ctx, nil, "model "+catalogID+" is not installed", http.StatusBadRequest)
	}

	// Reject reinstall of ONNX-dependent models when ORT is unavailable.
	if entry.RequiresONNX {
		ortStatus := c.checkORT(c.CurrentSettings().BirdNET.ONNXRuntimePath)
		if !ortStatus.Available {
			return c.HandleError(ctx, nil,
				"model requires ONNX Runtime "+inference.ORTRequiredVersion()+": "+ortStatus.Error,
				http.StatusConflict)
		}
	}

	// Start async reinstall in a background goroutine, which now owns the lease.
	handedOff = true
	progressChan := make(chan classifier.DownloadState, 16)
	c.Go(func() {
		defer lease.Release()
		defer c.ScheduleOptimizeNoticeSync() // see InstallModel
		defer func() {
			if r := recover(); r != nil {
				c.LogErrorIfEnabled("Panic during model reinstall",
					logger.String("catalog_id", catalogID),
					logger.Any("panic", r),
				)
			}
		}()
		if err := lease.Reinstall(c.Context(), &entry, "", progressChan); err != nil {
			c.LogErrorIfEnabled("Model reinstall failed",
				logger.String("catalog_id", catalogID),
				logger.Error(err),
			)
		} else {
			c.announceModelListChange()
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

// sumFileSizes returns the total size in bytes of files.
func sumFileSizes(files []classifier.CatalogFile) int64 {
	var total int64
	for _, f := range files {
		total += f.SizeBytes
	}
	return total
}

// UninstallModel removes a downloaded model from disk. It answers 409 with the
// removeHasDependentsKey i18n key when other installed models still need it, and
// with operationInProgressKey while another model operation runs. A successful
// uninstall announces the list change.
func (c *Handler) UninstallModel(ctx echo.Context) error {
	catalogID := ctx.Param("id")
	if catalogID == "" {
		return c.HandleError(ctx, nil, "catalog ID is required", http.StatusBadRequest)
	}

	if c.ModelManager == nil {
		return c.HandleError(ctx, nil, "model manager is not available", http.StatusServiceUnavailable)
	}

	wasInstalled := c.ModelManager.IsInstalled(catalogID)
	if err := c.ModelManager.Uninstall(catalogID); err != nil {
		if busy, ok := errors.AsType[*classifier.OperationInProgressError](err); ok {
			return c.operationInProgressResponse(ctx, err, busy)
		}
		if de, ok := errors.AsType[*classifier.DependentsError](err); ok {
			return c.HandleErrorWithKey(ctx, err, "model is required by installed models", http.StatusConflict,
				removeHasDependentsKey, map[string]any{
					"name":       de.Name,
					"models":     de.DependentNames(),
					"dependents": de.Dependents,
				})
		}
		// A failed file cleanup comes after the model left the installed set and
		// the config, so the list changed anyway: announce it before the error.
		// A refusal leaves the installed set as it was and announces nothing.
		if wasInstalled && !c.ModelManager.IsInstalled(catalogID) {
			c.ScheduleOptimizeNoticeSync()
			c.announceModelListChange()
		}
		return c.HandleError(ctx, err, "failed to uninstall model", http.StatusInternalServerError)
	}
	// Uninstalling a model that was not loaded fires no topology event, but it
	// can remove an optimize offer, so re-evaluate the notice here.
	c.ScheduleOptimizeNoticeSync()
	c.announceModelListChange()

	return ctx.JSON(http.StatusOK, map[string]string{
		"catalogId": catalogID,
		"status":    classifier.StatusRemoved,
	})
}

// StreamInstallProgress streams model download progress as Server-Sent Events.
// The stream closes automatically when the download completes or fails. Completion
// is reported only once the entry's operation has released the manager's operation
// slot, so a client that starts the next operation on it is not refused with 409.
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
					if c.ModelManager.OperationRunningFor(catalogID) {
						// Installed, but the operation still runs (the post-install
						// hot-load, or a reinstall not yet registered): wait for it.
						time.Sleep(apicore.SSEEventLoopSleep)
						continue
					}
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
