package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/logger"
)

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
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Author         string `json:"author"`
	License        string `json:"license"`
	CommercialUse  bool   `json:"commercialUse"`
	Category       string `json:"category"`
	Region         string `json:"region"`
	SpeciesCount   int    `json:"speciesCount"`
	Version        string `json:"version"`
	UpstreamURL    string `json:"upstreamUrl,omitempty"`
	Installed      bool   `json:"installed"`
	Compatible     bool   `json:"compatible"`
	TotalSizeBytes int64  `json:"totalSizeBytes"`
}

// initModelRoutes registers model-related API routes.
func (c *Controller) initModelRoutes() {
	c.Group.GET("/models", c.ListModels)
	c.Group.GET("/models/catalog", c.GetModelCatalog)
	c.Group.GET("/models/installed", c.GetInstalledModels)
	c.Group.POST("/models/install/:id", c.InstallModel, c.authMiddleware)
	c.Group.DELETE("/models/installed/:id", c.UninstallModel, c.authMiddleware)
	c.Group.GET("/models/install/:id/progress", c.StreamInstallProgress)
}

// ListModels returns classifier models that are enabled in the configuration.
func (c *Controller) ListModels(ctx echo.Context) error {
	// Build a set of enabled model config IDs for fast lookup.
	enabled := make(map[string]bool, len(c.Settings.Models.Enabled))
	for _, id := range c.Settings.Models.Enabled {
		enabled[strings.ToLower(id)] = true
	}

	models := make([]ModelListItem, 0, len(enabled))
	for id := range classifier.ModelRegistry {
		info := classifier.ModelRegistry[id]
		for _, alias := range info.ConfigAliases {
			if enabled[strings.ToLower(alias)] {
				// Determine category from catalog entry (if any), default to "bird".
				category := "bird"
				for i := range classifier.EmbeddedCatalog {
					if classifier.EmbeddedCatalog[i].RegistryID == id {
						category = classifier.EmbeddedCatalog[i].Category
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
func (c *Controller) GetModelCatalog(ctx echo.Context) error {
	catalog := make([]CatalogEntryResponse, 0, len(classifier.EmbeddedCatalog))

	for i := range classifier.EmbeddedCatalog {
		entry := &classifier.EmbeddedCatalog[i]

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

		catalog = append(catalog, CatalogEntryResponse{
			ID:             entry.ID,
			Name:           entry.Name,
			Description:    entry.Description,
			Author:         entry.Author,
			License:        entry.License,
			CommercialUse:  entry.CommercialUse,
			Category:       entry.Category,
			Region:         entry.Region,
			SpeciesCount:   entry.SpeciesCount,
			Version:        entry.Version,
			UpstreamURL:    entry.UpstreamURL,
			Installed:      installed,
			Compatible:     true, // build tag check deferred to a later task
			TotalSizeBytes: totalSize,
		})
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"catalog": catalog,
	})
}

// GetInstalledModels returns all models that have been downloaded and installed.
func (c *Controller) GetInstalledModels(ctx echo.Context) error {
	if c.ModelManager == nil {
		return ctx.JSON(http.StatusOK, []classifier.InstalledModel{})
	}

	return ctx.JSON(http.StatusOK, c.ModelManager.ListInstalled())
}

// InstallModel starts an asynchronous model download and installation.
// It returns 202 Accepted immediately while the download runs in the background.
func (c *Controller) InstallModel(ctx echo.Context) error {
	catalogID := ctx.Param("id")
	if catalogID == "" {
		return c.HandleError(ctx, nil, "catalog ID is required", http.StatusBadRequest)
	}

	entry, ok := classifier.GetCatalogEntry(catalogID)
	if !ok {
		return c.HandleError(ctx, nil, "unknown catalog ID: "+catalogID, http.StatusNotFound)
	}

	if c.ModelManager == nil {
		return c.HandleError(ctx, nil, "model manager is not available", http.StatusServiceUnavailable)
	}

	// Start async install in a background goroutine.
	progressChan := make(chan classifier.DownloadState, 16)
	c.wg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				c.logErrorIfEnabled("Panic during model install",
					logger.String("catalog_id", catalogID),
					logger.Any("panic", r),
				)
			}
		}()
		if err := c.ModelManager.Install(&entry, "", progressChan); err != nil {
			c.logErrorIfEnabled("Model install failed",
				logger.String("catalog_id", catalogID),
				logger.Error(err),
			)
		}
		close(progressChan)
		// Drain remaining progress events so the channel does not leak.
		for range progressChan {
		}
	})

	return ctx.JSON(http.StatusAccepted, map[string]string{
		"catalogId": catalogID,
		"status":    classifier.StatusDownloading,
	})
}

// UninstallModel removes a downloaded model from disk.
func (c *Controller) UninstallModel(ctx echo.Context) error {
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
func (c *Controller) StreamInstallProgress(ctx echo.Context) error {
	catalogID := ctx.Param("id")
	if catalogID == "" {
		return c.HandleError(ctx, nil, "catalog ID is required", http.StatusBadRequest)
	}

	if c.ModelManager == nil {
		return c.HandleError(ctx, nil, "model manager is not available", http.StatusServiceUnavailable)
	}

	// Set SSE headers.
	setSSEHeaders(ctx)

	// Flush helper for the response writer.
	flusher, ok := ctx.Response().Writer.(http.Flusher)
	if !ok {
		return c.HandleError(ctx, nil, "streaming not supported", http.StatusInternalServerError)
	}

	ticker := time.NewTicker(sseHeartbeatInterval)
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
				time.Sleep(sseEventLoopSleep)
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
			time.Sleep(sseEventLoopSleep)
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
