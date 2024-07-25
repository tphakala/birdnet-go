package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// searchHandler handles the search functionality.
// It searches for notes based on a query and renders the results.
func (h *Handlers) SearchDetections(c echo.Context) error {
	searchQuery := c.QueryParam("query")
	if searchQuery == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Search query is required.")
	}

	// Number of results to return
	numResults := parseNumDetections(c.QueryParam("numResults"), 25) // default 25

	// Pagination: Calculate offset
	offset := parseOffset(c.QueryParam("offset"), 0) // default 25

	// Query the database with the new offset
	notes, err := h.DS.SearchNotes(searchQuery, false, numResults, offset)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
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

	// render the searchResults template with the data
	return c.Render(http.StatusOK, "searchResults", data)
}
