package handlers

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// searchHandler handles the search functionality.
// It searches for notes based on a query and renders the results.
func (h *Handlers) SearchDetections(c echo.Context) error {
	searchQuery := c.QueryParam("query")
	if searchQuery == "" {
		return h.NewHandlerError(errors.New("empty search query"), "Search query is required", http.StatusBadRequest)
	}

	// Number of results to return
	numResults := parseNumDetections(c.QueryParam("numResults"), 25) // default 25

	// Pagination: Calculate offset
	offset := parseOffset(c.QueryParam("offset"), 0) // default 0

	// Query the database with the new offset
	notes, err := h.DS.SearchNotes(searchQuery, false, numResults, offset)
	if err != nil {
		return h.NewHandlerError(err, "Failed to search notes", http.StatusInternalServerError)
	}

	// Prepare data for rendering in the template
	data := struct {
		Notes       []datastore.Note
		SearchQuery string
		NumResults  int
		Offset      int
	}{
		Notes:       notes,
		SearchQuery: searchQuery,
		NumResults:  numResults,
		Offset:      offset,
	}

	// Render the searchResults template with the data
	if err := c.Render(http.StatusOK, "searchDetections", data); err != nil {
		return h.NewHandlerError(err, "Failed to render search results", http.StatusInternalServerError)
	}

	return nil
}
