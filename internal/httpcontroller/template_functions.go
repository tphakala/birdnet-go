// internal/httpcontroller/template_functions.go
package httpcontroller

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// GetTemplateFunctions returns a map of functions that can be used in templates
func (s *Server) GetTemplateFunctions() template.FuncMap {
	return template.FuncMap{
		"sub":                   subFunc,
		"add":                   addFunc,
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
	}
}

// simple math functions
func subFunc(a, b int) int { return a - b }
func addFunc(a, b int) int { return a + b }

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
	case ".aac", ".m4a":
		return "audio/aac"
	default:
		return "audio/mpeg" // Default to MP3 if unknown
	}
}

// urlSafe converts a path to a slash format and encodes it for URL query
func urlSafe(path string) string {
	return url.QueryEscape(filepath.ToSlash(path))
}
