// internal/httpcontroller/template_functions.go
package httpcontroller

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
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
		"sunPositionIcon":       s.Handlers.GetSunPositionIconFunc(),
		"weatherIcon":           s.Handlers.GetWeatherIconFunc(),
		"timeOfDayToInt":        s.Handlers.TimeOfDayToInt,
		"getAudioMimeType":      getAudioMimeType,
		"urlsafe":               urlSafe,
		"ffmpegAvailable":       conf.IsFfmpegAvailable,
		"formatDateTime":        formatDateTime,
		"getHourlyCounts":       getHourlyCounts,
		"sumHourlyCountsRange":  sumHourlyCountsRange,
	}
}

/**
 * addFunc calculates the sum of the input integers.
 *
 * @param numbers The integers to be summed up.
 * @return The total sum of the input integers.
 */
func addFunc(numbers ...int) int {
	sum := 0
	for _, num := range numbers {
		sum += num
	}
	return sum
}
func subFunc(a, b int) int { return a - b }
func divFunc(a, b int) int { return a / b }
func modFunc(a, b int) int { return a % b }

/**
 * dictFunc creates a dictionary from key-value pairs provided as arguments.
 *
 * @param values A variadic parameter list of key-value pairs. Keys must be strings.
 * @return A map[string]interface{} representing the dictionary created.
 * An error if the number of arguments is odd or if keys are not strings.
 */
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

/**
 * seqFunc generates a sequence of integers starting from 'start' to 'end' (inclusive).
 *
 * @param start The starting integer of the sequence
 * @param end The ending integer of the sequence
 * @return []int The generated sequence of integers
 */
func seqFunc(start, end int) []int {
	seq := make([]int, end-start+1)
	for i := range seq {
		seq[i] = start + i
	}
	return seq
}

/**
 * getHourlyCounts returns a map containing hourly counts data for a given element.
 *
 * @param element handlers.NoteWithIndex - The element for which hourly counts are generated.
 * @param hourIndex int - The index representing the hour for which counts are calculated.
 * @return map[string]interface{} - A map with HourIndex and Name fields.
 */
func getHourlyCounts(element handlers.NoteWithIndex, hourIndex int) map[string]interface{} {
	baseData := map[string]interface{}{
		"HourIndex": hourIndex,
		"Name":      element.Note.CommonName,
	}

	return baseData
}

/**
 * sumHourlyCountsRange calculates the sum of counts within a specified range of hours.
 *
 * @param counts An array containing hourly counts.
 * @param start The starting hour index of the range.
 * @param length The length of the range in hours.
 * @return The sum of counts within the specified range.
 */
func sumHourlyCountsRange(counts [24]int, start, length int) int {
	sum := 0
	for i := start; i < start+length; i++ {
		sum += counts[i%24]
	}
	return sum
}
