// perch.go provides Perch v2 model support.
// Label parsing is available in all builds.
// ONNX inference requires the onnx build tag (see perch_onnx.go).
package classifier

import (
	"bufio"
	"bytes"
	"strings"
)

// perchDatasetMarker is the first line of Perch label files, identifying the dataset.
const perchDatasetMarker = "inat2024_fsd50k"

// ParsePerchLabels parses a Perch v2 label file.
// Format: one scientific name per line. Line 1 is a dataset marker that is skipped.
// Empty lines are skipped. Returns the list of scientific names.
func ParsePerchLabels(data []byte) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var labels []string
	firstLine := true

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Skip the dataset marker header line
		if firstLine {
			firstLine = false
			if line == perchDatasetMarker {
				continue
			}
		}
		labels = append(labels, line)
	}

	return labels, scanner.Err()
}
