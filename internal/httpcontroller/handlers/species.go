package handlers

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// IgnoreSpecies adds or removes a species from the excluded species list
func (h *Handlers) IgnoreSpecies(c echo.Context) error {
	commonName := c.QueryParam("common_name")
	if commonName == "" {
		h.SSE.SendNotification(Notification{
			Message: "Missing species name",
			Type:    "error",
		})
		return h.NewHandlerError(fmt.Errorf("missing species name"), "Missing species name", http.StatusBadRequest)
	}

	// Get settings instance
	settings := conf.Setting()

	// Check if species is already in the excluded list
	isExcluded := false
	for _, s := range settings.Realtime.Species.Exclude {
		if s == commonName {
			isExcluded = true
			break
		}
	}

	if isExcluded {
		// Remove from excluded list
		newExcludeList := make([]string, 0)
		for _, s := range settings.Realtime.Species.Exclude {
			if s != commonName {
				newExcludeList = append(newExcludeList, s)
			}
		}
		settings.Realtime.Species.Exclude = newExcludeList
	} else {
		// Add to excluded list
		settings.Realtime.Species.Exclude = append(settings.Realtime.Species.Exclude, commonName)
	}

	// Save the settings
	if err := conf.SaveSettings(); err != nil {
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to save settings: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to save settings", http.StatusInternalServerError)
	}

	// Send success notification
	message := fmt.Sprintf("%s %s excluded species list", commonName, map[bool]string{true: "removed from", false: "added to"}[isExcluded])
	h.SSE.SendNotification(Notification{
		Message: message,
		Type:    "success",
	})

	return c.NoContent(http.StatusOK)
}
