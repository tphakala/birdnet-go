// internal/httpcontroller/template_functions.go
package httpcontroller

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/observation"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// GetTemplateFunctions returns a map of functions that can be used in templates
func (s *Server) GetTemplateFunctions() template.FuncMap {
	return template.FuncMap{
		"add":                   addFunc,
		"sub":                   subFunc,
		"div":                   divFunc,
		"mod":                   modFunc,
		"seq":                   seqFunc,
		"dict":                  dictFunc,
		"even":                  even,
		"calcWidth":             calcWidth,
		"heatmapColor":          heatmapColor,
		"title":                 cases.Title(language.English).String,
		"confidence":            confidence,
		"confidenceColor":       confidenceColor,
		"thumbnail":             s.Handlers.Thumbnail,
		"thumbnailAttribution":  s.Handlers.ThumbnailAttribution,
		"RenderContent":         s.RenderContent,
		"renderSettingsContent": s.renderSettingsContent,
		"toJSON":                toJSONFunc,
		"safeJSON":              safeJSONFunc,
		"sunPositionIcon":       s.Handlers.GetSunPositionIconFunc(),
		"weatherIcon":           s.Handlers.GetWeatherIconFunc(),
		"timeOfDayToInt":        s.Handlers.TimeOfDayToInt,
		"getAudioMimeType":      getAudioMimeType,
		"urlsafe":               urlSafe,
		"ffmpegAvailable":       conf.IsFfmpegAvailable,
		"formatDateTime":        formatDateTime,
		"getHourlyHeaderData":   getHourlyHeaderData,
		"getHourlyCounts":       getHourlyCounts,
		"sumHourlyCountsRange":  sumHourlyCountsRange,
		"weatherDescription":    s.Handlers.GetWeatherDescriptionFunc(),
		"getAllSpecies":         s.GetAllSpecies,
		"getIncludedSpecies":    s.GetIncludedSpecies,
		"isSpeciesExcluded": func(commonName string) bool {
			settings := conf.Setting()
			for _, s := range settings.Realtime.Species.Exclude {
				if s == commonName {
					return true
				}
			}
			return false
		},
	}
}

// addFunc calculates the sum of the input integers.
// Parameters:
//   - numbers: Variadic list of integers or strings representing integers
//
// Returns:
//
//	The total sum of all input numbers
func addFunc(numbers ...interface{}) int {
	sum := 0
	for _, num := range numbers {
		switch v := num.(type) {
		case int:
			sum += v
		case string:
			if i, err := strconv.Atoi(v); err == nil {
				sum += i
			}
		}
	}
	return sum
}
func subFunc(a, b int) int { return a - b }
func divFunc(a, b int) int { return a / b }
func modFunc(a, b int) int { return a % b }

// dictFunc creates a dictionary from key-value pairs.
// Parameters:
//   - values: Variadic list of alternating string keys and interface{} values
//
// Returns:
//   - map[string]interface{}: Dictionary with provided key-value pairs
//   - error: Invalid dict call or non-string keys
func dictFunc(values ...interface{}) (map[string]interface{}, error) {
	if len(values)%2 != 0 {
		return nil, errors.New("invalid dict call")
	}
	dict := make(map[string]interface{}, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, errors.New("dict keys must be strings")
		}
		dict[key] = values[i+1]
	}
	return dict, nil
}

// toJSONFunc converts a value to a JSON string
func toJSONFunc(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// calcWidth calculates the width of a bar in a bar chart as a percentage.
// It normalizes the totalDetections based on a predefined maximum.
func calcWidth(totalDetections int) int {
	const maxDetections = 200 // Maximum number of detections expected
	widthPercentage := (totalDetections * 100) / maxDetections
	if widthPercentage > 100 {
		widthPercentage = 100 // Limit width to 100% if exceeded
	}
	return widthPercentage
}

// even checks if an integer is even. Useful for alternating styles in loops.
func even(index int) bool {
	return index%2 == 0
}

// heatmapColor assigns a color based on a provided value using predefined thresholds.
func heatmapColor(value int) string {
	if value == 0 {
		return "0"
	}
	thresholds := []int{10, 20, 30, 40, 50, 60, 70, 80, 90}
	colors := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

	for i, threshold := range thresholds {
		if value <= threshold {
			return colors[i]
		}
	}
	return colors[len(colors)-1] // Default to the highest color for values above all thresholds
}

// confidence converts a confidence value (0.0 - 1.0) to a percentage string.
func confidence(confidence float64) string {
	return fmt.Sprintf("%.0f%%", confidence*100)
}

// confidenceColor assigns a color based on the confidence value.
func confidenceColor(confidence float64) string {
	switch {
	case confidence >= 0.8:
		return "bg-green-500" // High confidence
	case confidence >= 0.4:
		return "bg-orange-400" // Moderate confidence
	default:
		return "bg-red-500" // Low confidence
	}
}

// getAudioMimeType returns the MIME type of an audio file based on its extension.
func getAudioMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".aac":
		return "audio/aac"
	case ".m4a":
		return "audio/mp4"
	case ".alac":
		return "audio/x-alac"
	default:
		return "audio/mpeg" // Default to MP3 if unknown
	}
}

// urlSafe converts a path to a slash format and encodes it for URL query
func urlSafe(path string) string {
	return url.QueryEscape(filepath.ToSlash(path))
}

// formatDateTime converts a date string to a formatted string
func formatDateTime(dateStr string) string {
	t, err := time.Parse("2006-01-02 15:04:05", dateStr)
	if err != nil {
		return dateStr // Return original string if parsing fails
	}
	return t.Format("2006-01-02 15:04:05") // Or any other format you prefer
}

// seqFunc generates a sequence of integers.
// Parameters:
//   - start: First integer in sequence
//   - end: Last integer in sequence (inclusive)
//
// Returns:
//
//	[]int: Generated sequence from start to end
func seqFunc(start, end int) []int {
	seq := make([]int, end-start+1)
	for i := range seq {
		seq[i] = start + i
	}
	return seq
}

// getHourlyHeaderData constructs a map containing metadata for a specific hour.
// Parameters:
//   - hourIndex: The index of the hour (0-23)
//   - class: CSS class name for styling ("hourly-count", "bi-hourly-count", "six-hourly-count")
//   - length: Time period length in hours (1, 2, or 6)
//   - date: Date string in YYYY-MM-DD format
//   - sunrise: Hour index when sunrise occurs
//   - sunset: Hour index when sunset occurs
//
// Returns:
//
//	A map containing the hour metadata with keys:
//	"Class", "Length", "HourIndex", "Date", "Sunrise", "Sunset"
func getHourlyHeaderData(hourIndex int, class string, length int, date string, sunrise, sunset int) map[string]interface{} {
	baseData := map[string]interface{}{
		"Class":     class,
		"Length":    length,
		"HourIndex": hourIndex,
		"Date":      date,
		"Sunrise":   sunrise,
		"Sunset":    sunset,
	}
	return baseData
}

// getHourlyCounts returns hourly count data for a detection.
// Parameters:
//   - element: NoteWithIndex containing detection data
//   - hourIndex: Hour index (0-23) to get counts for
//
// Returns:
//
//	map[string]interface{} with HourIndex and species Name
func getHourlyCounts(element *handlers.NoteWithIndex, hourIndex int) map[string]interface{} {
	baseData := map[string]interface{}{
		"HourIndex": hourIndex,
		"Name":      element.Note.CommonName,
	}

	return baseData
}

// sumHourlyCountsRange calculates sum of counts in hour range.
// Parameters:
//   - counts: 24-hour array of detection counts
//   - start: Starting hour index
//   - length: Number of hours to sum
//
// Returns:
//
//	Sum of counts within specified range
func sumHourlyCountsRange(counts *[24]int, start, length int) int {
	sum := 0
	for i := start; i < start+length; i++ {
		sum += counts[i%24]
	}
	return sum
}

// safeJSONFunc converts a value to a safely escaped JSON string for use in HTML templates
func safeJSONFunc(v interface{}) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		return template.JS("null")
	}

	// Convert to string and HTML-escape the JSON
	s := template.JSEscapeString(string(b))

	// Additional escaping for quotes in HTML attributes
	s = strings.ReplaceAll(s, "'", "\\'")

	return template.JS(s)
}

// GetIncludedSpecies returns a deduplicated list of included species
func (s *Server) GetIncludedSpecies() []string {
	var preparedSpecies []string
	var scientificNames []string
	var commonNames []string

	// Split species entry into scientific and common names
	for _, species := range s.Settings.BirdNET.RangeFilter.Species {
		parts := strings.Split(species, "_")
		if len(parts) >= 2 {
			scientificNames = append(scientificNames, strings.TrimSpace(parts[0]))
			commonNames = append(commonNames, strings.TrimSpace(parts[1]))
		}
	}

	// Sort both slices alphabetically
	sort.Strings(scientificNames)
	sort.Strings(commonNames)

	// Combine common names first, then scientific names
	preparedSpecies = append(preparedSpecies, commonNames...)
	preparedSpecies = append(preparedSpecies, scientificNames...)

	return removeDuplicates(preparedSpecies)
}

// GetAllSpecies returns a deduplicated list of all available species
func (s *Server) GetAllSpecies() []string {
	// Create a map to track unique species
	uniqueSpecies := make(map[string]bool)

	// Get all labels from the handlers which has access to BirdNET labels through settings
	for _, label := range s.Handlers.GetLabels() {
		// Parse the species string to get both scientific and common names
		scientificName, commonName, _ := observation.ParseSpeciesString(label)

		// Add both names to the unique map
		uniqueSpecies[scientificName] = true
		uniqueSpecies[commonName] = true
	}

	// Convert map keys to sorted slice
	result := make([]string, 0, len(uniqueSpecies))
	for species := range uniqueSpecies {
		if species != "" { // Skip empty strings
			result = append(result, species)
		}
	}

	// Sort the slice alphabetically
	sort.Strings(result)

	return result
}
