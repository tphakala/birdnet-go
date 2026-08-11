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
	hardwareProfile func(onnxRuntimePath string) hwprofile.Profile
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
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Author             string `json:"author"`
	License            string `json:"license"`
	CommercialUse      bool   `json:"commercialUse"`
	Category           string `json:"category"`
	Region             string `json:"region"`
	SpeciesCount       int    `json:"speciesCount"`
	Version            string `json:"version"`
	UpstreamURL        string `json:"upstreamUrl,omitempty"`
	Installed          bool   `json:"installed"`
	Compatible         bool   `json:"compatible"`
	IncompatibleReason string `json:"incompatibleReason,omitempty"`
	TotalSizeBytes     int64  `json:"totalSizeBytes"`
	HasGeomodel        bool   `json:"hasGeomodel"`
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
	ID                string `json:"id"`
	Region            string `json:"region,omitempty"`
	Precision         string `json:"precision,omitempty"`
	SpeciesCount      int    `json:"speciesCount"`
	Default           bool   `json:"default"`
	Installed         bool   `json:"installed"`
	SizeBytes         int64  `json:"sizeBytes"`
	HeadlineLatencyMs int    `json:"headlineLatencyMs,omitempty"`
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
	if c.recommendationsAllowed(ctx) {
		recsByVariant, recommendedVariant = c.rankCatalog(visible)
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
			incompatibleReason = ortStatus.Error
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
			UpstreamURL:          entry.UpstreamURL,
			Installed:            installed,
			Compatible:           compatible,
			IncompatibleReason:   incompatibleReason,
			TotalSizeBytes:       totalSize,
			HasGeomodel:          classifier.HasGeomodelFiles(entry),
			InstalledVariantID:   installedVariantID,
			RecommendedVariantID: recommendedVariant[entry.ID],
			Variants:             buildVariantResponses(entry, installed, installedVariantID, recsByVariant[entry.ID]),
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
func buildVariantResponses(entry *classifier.CatalogEntry, installed bool, installedVariantID string, recs map[string]recommend.Recommendation) []CatalogVariantResponse {
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
			SizeBytes:         sizeBytes,
			HeadlineLatencyMs: headlineLatencyMs,
			// Neutral default: without a recommendation the variant is not claimed
			// incompatible. Overwritten below when recommendations are present.
			Compatible: true,
		}
		if rec, ok := recs[v.ID]; ok {
			resp.Compatible = rec.Compatible
			resp.Recommended = rec.Recommended
			resp.Reasons = toReasonResponses(rec.Reasons)
			resp.Blockers = toReasonResponses(rec.Blockers)
		}
		variants = append(variants, resp)
	}
	return variants
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
func (c *Handler) rankCatalog(entries []classifier.CatalogEntry) (byVariant map[string]map[string]recommend.Recommendation, recommended map[string]string) {
	profileFn := c.hardwareProfile
	if profileFn == nil {
		profileFn = defaultHardwareProfile
	}
	profile := profileFn(c.CurrentSettings().BirdNET.ONNXRuntimePath)
	recs := recommend.Rank(recommend.Input{
		Capabilities:  profile.Capabilities(),
		TotalRAMBytes: profile.TotalRAMBytes,
		DeviceClass:   recommend.DeviceClass(profile.Board.Tier, profile.Arch),
		Entries:       entries,
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
	return byVariant, recommended
}

// defaultHardwareProfile resolves the live host profile with per-request backend
// probes, mirroring the inference-status endpoint (internal/api/v2/system/
// inference_status.go): the ONNX Runtime path comes from settings so a
// hot-reloaded runtime path is honored, and the OpenVINO device list feeds GPU
// capability derivation. It is the production value of the hardwareProfile seam.
func defaultHardwareProfile(onnxRuntimePath string) hwprofile.Profile {
	ort := inference.CheckORTAvailability(onnxRuntimePath)
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

	// Hidden entries are foundation-only: excluded from the gallery and not meant
	// to be installed by ID. Some (the DFT-truncated BirdNET v2.4 variants) carry
	// the permanent registry ID, which Uninstall then refuses, so an inadvertent
	// install would leave an unremovable, unused model on disk. Reject them here.
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

	// Reject installation of ONNX-dependent models when ORT is unavailable.
	if entry.RequiresONNX {
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
