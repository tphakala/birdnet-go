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
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Handler serves the models domain endpoints. It embeds *apicore.Core BY
// POINTER so the shared Core members promote onto it without re-wiring; Core
// carries atomic/lock-bearing fields and must never be copied by value.
type Handler struct {
	*apicore.Core
}

// New builds a models Handler around the shared core. The models handlers need
// only the shared *apicore.Core (ModelManager, settings, error/log helpers and
// the goroutine plumbing), so there are no facade-owned dependencies to inject.
func New(core *apicore.Core) *Handler {
	return &Handler{Core: core}
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

	for i := range visible {
		entry := &visible[i]

		// Compute total size from all files.
		var totalSize int64
		for _, f := range entry.Files {
			totalSize += f.SizeBytes
		}

		// Check install status via ModelManager.
		installed := false
		if c.ModelManager != nil {
			installed = c.ModelManager.IsInstalled(entry.ID)
		}

		// Models requiring ONNX Runtime are incompatible when ORT is absent.
		compatible := true
		incompatibleReason := ""
		if entry.RequiresONNX && !ortStatus.Available {
			compatible = false
			incompatibleReason = ortStatus.Error
		}

		catalog = append(catalog, CatalogEntryResponse{
			ID:                 entry.ID,
			Name:               entry.Name,
			Description:        entry.Description,
			Author:             entry.Author,
			License:            entry.License,
			CommercialUse:      entry.CommercialUse,
			Category:           entry.Category,
			Region:             entry.Region,
			SpeciesCount:       entry.SpeciesCount,
			Version:            entry.Version,
			UpstreamURL:        entry.UpstreamURL,
			Installed:          installed,
			Compatible:         compatible,
			IncompatibleReason: incompatibleReason,
			TotalSizeBytes:     totalSize,
			HasGeomodel:        classifier.HasGeomodelFiles(entry),
		})
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"catalog": catalog,
	})
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
		if err := c.ModelManager.Install(c.Context(), &entry, "", progressChan); err != nil {
			c.LogErrorIfEnabled("Model install failed",
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
