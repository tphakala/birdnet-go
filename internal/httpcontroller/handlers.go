// httpcontroller/handlers.go
package httpcontroller

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// NoteWithSpectrogram extends the Note model with a spectrogram path.
type NoteWithSpectrogram struct {
	datastore.Note
	SpectrogramPath string
}

// LocaleData represents a locale with its code and full name.
type LocaleData struct {
	Code string
	Name string
}

// RenderContentForPage renders content for a given page using Echo's renderer.
// This method is used to render dynamic content within the page templates.
func (s *Server) RenderContent(data interface{}) (template.HTML, error) {
	d, ok := data.(struct {
		C        echo.Context
		Page     string
		Title    string
		Settings *conf.Settings
		Locales  []LocaleData
	})
	if !ok {
		return "", fmt.Errorf("invalid data type")
	}

	c := d.C // Extracted context

	// Find the current route configuration based on the request URL.
	var currentRoute *routeConfig
	for _, route := range routes {
		if route.Path == c.Request().URL.Path {
			currentRoute = &route
			break
		}
	}

	if currentRoute == nil {
		currentRoute = &routeConfig{TemplateName: "dashboard"}
	}

	buf := new(bytes.Buffer)
	// Passing the 'data' to the template renderer.
	err := s.Echo.Renderer.Render(buf, currentRoute.TemplateName, d, c)
	if err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

// handleRequest handles generic route requests.
// It identifies the current route and renders the appropriate template.
func (s *Server) handleRequest(c echo.Context) error {
	var currentRoute *routeConfig
	for _, route := range routes {
		if route.Path == c.Request().URL.Path {
			currentRoute = &route
			break
		}
	}

	if currentRoute == nil {
		currentRoute = &routeConfig{TemplateName: "dashboard", Title: "Dashboard"}
	}

	data := struct {
		C        echo.Context
		Page     string
		Title    string
		Settings *conf.Settings
		Locales  []LocaleData
	}{
		C:        c,
		Page:     currentRoute.TemplateName,
		Title:    currentRoute.Title,
		Settings: s.Settings, // Pass the settings from the server
	}

	// Include settings and locales data only for the settings page
	if currentRoute.TemplateName == "settings" {
		var locales []LocaleData
		for code, name := range conf.LocaleCodes {
			locales = append(locales, LocaleData{Code: code, Name: name})
		}

		// Sort locales alphabetically by Name
		sort.Slice(locales, func(i, j int) bool {
			return locales[i].Name < locales[j].Name
		})

		data.Locales = locales
	}

	return c.Render(http.StatusOK, "index", data)
}

// topBirdsHandler handles requests for the top bird sightings.
// It retrieves data based on the specified date and minimum confidence,
// then renders it using the 'birdsTableHTML' template.
func (s *Server) topBirdsHandler(c echo.Context) error {
	// Retrieving query parameters
	selectedDate := c.QueryParam("date")
	if selectedDate == "" {
		selectedDate = getCurrentDate()
	}

	minConfidenceStr := c.QueryParam("minConfidence")
	minConfidence, err := strconv.ParseFloat(minConfidenceStr, 64)
	if err != nil {
		minConfidence = 0.0 // Default value on error
	}
	minConfidenceNormalized := minConfidence / 100.0

	// Get top birds data from the database
	notes, err := s.ds.GetTopBirdsData(selectedDate, minConfidenceNormalized)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Process notes with additional data such as hourly occurrences and total detections
	notesWithIndex, err := s.processNotes(notes, selectedDate, minConfidenceNormalized)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Sorting the notes by total detections in descending order
	sort.Slice(notesWithIndex, func(i, j int) bool {
		return notesWithIndex[i].TotalDetections > notesWithIndex[j].TotalDetections
	})

	// Creating a slice with hours from 0 to 23
	hours := makeHoursSlice()

	// Preparing data for rendering in the template
	data := struct {
		NotesWithIndex []NoteWithIndex
		Hours          []int
		SelectedDate   string
	}{
		NotesWithIndex: notesWithIndex,
		Hours:          hours,
		SelectedDate:   selectedDate,
	}

	// Render the birdsTableHTML template with the data
	return c.Render(http.StatusOK, "birdsTableHTML", data)
}

// Additional helper functions (processNotes, makeHoursSlice, updateClipNames, etc.) go here...
func (s *Server) processNotes(notes []datastore.Note, selectedDate string, minConfidenceNormalized float64) ([]NoteWithIndex, error) {
	startTime := time.Now()
	notesWithIndex := make([]NoteWithIndex, 0, len(notes))
	for _, note := range notes {
		//noteStartTime := time.Now() // Start timing for this note

		hourlyCounts, err := s.ds.GetHourlyOccurrences(selectedDate, note.CommonName, minConfidenceNormalized)
		if err != nil {
			return nil, err // Return error to be handled by the caller
		}

		//log.Printf("Time to fetch hourly occurrences for note %s: %v", note.CommonName, time.Since(noteStartTime)) // Print the time taken for this part

		totalDetections := sumHourlyCounts(hourlyCounts)

		notesWithIndex = append(notesWithIndex, NoteWithIndex{
			Note:            note,
			HourlyCounts:    hourlyCounts,
			TotalDetections: totalDetections,
		})
		//log.Printf("Total time for processing note %s: %v", note.CommonName, time.Since(noteStartTime)) // Print the total time for this note

	}
	log.Printf("Total time for processing all notes: %v", time.Since(startTime)) // Print the total time for the function

	return notesWithIndex, nil
}

// speciesDetectionsHandler handles requests for species-specific detections.
// It retrieves detection data for a given species and date, then renders it.
func (s *Server) speciesDetectionsHandler(c echo.Context) error {
	species, date, hour := c.QueryParam("species"), c.QueryParam("date"), c.QueryParam("hour")

	if species == "" || date == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Species and date parameters are required.")
	}

	notes, err := s.ds.SpeciesDetections(species, date, hour, false)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Prepare data for rendering in the template
	data := struct {
		Notes []datastore.Note
	}{
		Notes: notes,
	}

	return c.Render(http.StatusOK, "speciesDetections", data)
}

// deleteNoteHandler deletes note object from database and its associated audio file
func (s *Server) deleteNoteHandler(c echo.Context) error {
	noteID := c.QueryParam("id")
	if noteID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Note ID is required.")
	}

	// Retrieve the path to the audio file before deleting the note
	clipPath, err := s.ds.GetNoteClipPath(noteID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to retrieve audio clip path: "+err.Error())
	}

	// Delete the note from the database
	err = s.ds.Delete(noteID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete note: "+err.Error())
	}

	// If there's an associated clip, delete the file
	if clipPath != "" {
		err = os.Remove(clipPath)
		if err != nil {
			log.Println("Failed to delete audio clip: ", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete audio clip: "+err.Error())
		} else {
			log.Println("Deleted audio clip: ", clipPath)
		}
	}

	// Pass this struct to the template or return a success message
	return c.HTML(http.StatusOK, `<div x-data="{ show: true }" x-show="show" x-init="setTimeout(() => show = false, 3000)" class="notification-class">Delete successful!</div>`)
}

// searchHandler handles the search functionality.
// It searches for notes based on a query and renders the results.
func (s *Server) searchHandler(c echo.Context) error {
	searchQuery := c.QueryParam("query")
	if searchQuery == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Search query is required.")
	}

	// Number of results to return
	numResults := parseNumDetections(c.QueryParam("numResults"), 25) // default 25

	// Pagination: Calculate offset
	offset := parseOffset(c.QueryParam("offset"), 0) // default 25

	// Query the database with the new offset
	notes, err := s.ds.SearchNotes(searchQuery, false, numResults, offset)
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
		Offset:      offset, // Prepare next offset
	}

	// Pass this struct to the template
	return c.Render(http.StatusOK, "searchResults", data)
}

// GetLastDetections handles requests for the latest detections.
// It retrieves the last set of detections based on the specified count.
func (s *Server) GetLastDetections(c echo.Context) error {
	numDetections := parseNumDetections(c.QueryParam("numDetections"), 10) // Default value is 10

	notes, err := s.ds.GetLastDetections(numDetections)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Error fetching detections"})
	}

	notesWithSpectrogram, err := wrapNotesWithSpectrogram(notes)
	if err != nil {
		// Handle the error appropriately
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.Render(http.StatusOK, "recentDetections", notesWithSpectrogram)
}

// getAllNotes retrieves all notes from the database.
// It returns the notes in a JSON format.
func (s *Server) GetAllNotes(c echo.Context) error {
	notes, err := s.ds.GetAllNotes()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err)
	}

	return c.JSON(http.StatusOK, notes)
}
