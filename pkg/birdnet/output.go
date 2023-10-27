package birdnet

import (
	"fmt"
	"sort"
	"strings"
)

type Result struct {
	Species    string
	Confidence float32
}

type DetectionsMap map[string][]Result

// If the input is "Cyanocitta_cristata_Blue Jay", the function will return "Blue Jay".
// If there's no underscore in the string or if the format is unexpected, it returns the input string itself.
func ExtractCommonName(species string) string {
	parts := strings.Split(species, "_")
	if len(parts) > 1 {
		return parts[1]
	}
	return species
}

// PrintDetectionsWithThreshold displays a list of detected species with their corresponding
// time intervals and confidence percentages. Only detections with confidence above the given
// threshold (e.g., 0.1 or 10%) are displayed.
func PrintDetectionsWithThreshold(detections DetectionsMap, threshold float32) {
	// Extract the keys (time intervals) from the map and sort them
	var intervals []string
	for interval := range detections {
		intervals = append(intervals, interval)
	}
	sort.Strings(intervals)

	for _, interval := range intervals {
		detectedPairs := detections[interval]
		var validDetections []string
		for _, pair := range detectedPairs {
			if pair.Confidence >= threshold {
				commonName := ExtractCommonName(pair.Species)
				validDetections = append(validDetections, fmt.Sprintf("%-30s %.1f%%", commonName, pair.Confidence*100))
			}
		}
		// Only print the interval if there are valid detections for it.
		if len(validDetections) > 0 {
			fmt.Printf("Time Interval: %s", interval)
			for _, detection := range validDetections {
				fmt.Printf("\t%s\n", detection)
			}
		}
	}
}
