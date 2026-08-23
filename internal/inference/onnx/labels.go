package onnx

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/csvutil"
)

// LoadLabels reads species labels from a file. Supports .csv (auto-detects
// label column) and .json (array or object formats). Files with .txt or
// unknown/missing extensions are parsed as plain text (one label per line).
func LoadLabels(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Path provided by caller
	if err != nil {
		return nil, &LabelLoadError{Path: path, Reason: err.Error()}
	}
	ext := strings.ToLower(filepath.Ext(path))
	labels, err := loadLabelsFromBytes(data, ext)
	if err != nil {
		return nil, &LabelLoadError{Path: path, Reason: err.Error()}
	}
	return labels, nil
}

func loadLabelsFromBytes(data []byte, ext string) ([]string, error) {
	switch ext {
	case ".csv":
		return loadLabelsCSV(data)
	case ".json":
		return loadLabelsJSON(data)
	default:
		return loadLabelsText(data)
	}
}

// Label scanner buffer sizes. A label line is short ("SciName_CommonName"),
// but the buffer is grown past bufio's default 64 KiB token cap so an overlong
// or newline-free label file does not fail with "bufio.Scanner: token too long"
// (Sentry BIRDNET-GO-2FF). Defined locally because the onnx package must not
// import the classifier package where the sibling constants live.
const (
	// maxLabelLineBytes is the largest label line we support (1 MiB).
	maxLabelLineBytes           = 1024 * 1024
	labelScannerInitialBufBytes = 64 * 1024 // initial scan buffer (64 KiB)
	// labelScannerMaxLineBytes is the bufio.Scanner token cap, maxLabelLineBytes
	// + 2 because Scanner needs its max strictly greater than the longest token
	// (it must read past the line, or hit EOF, to terminate it): a line of
	// exactly maxLabelLineBytes, with or without a CR/LF terminator, otherwise
	// fails with "bufio.Scanner: token too long".
	labelScannerMaxLineBytes = maxLabelLineBytes + 2
)

func loadLabelsText(data []byte) ([]string, error) {
	var labels []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, labelScannerInitialBufBytes), labelScannerMaxLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			labels = append(labels, line)
		}
	}
	return labels, scanner.Err()
}

func loadLabelsCSV(data []byte) ([]string, error) {
	firstLine, _, _ := bytes.Cut(data, []byte("\n"))
	delimiter := ','
	if bytes.Count(firstLine, []byte(";")) > bytes.Count(firstLine, []byte(",")) {
		delimiter = ';'
	}

	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = delimiter
	r.LazyQuotes = true

	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("invalid CSV: need header plus at least one data row, got %d rows", len(records))
	}

	header := records[0]
	colIdx, err := findLabelColumn(header)
	if err != nil {
		return nil, err
	}

	var labels []string
	for _, row := range records[1:] {
		if colIdx < len(row) {
			label := strings.TrimSpace(row[colIdx])
			if label != "" {
				labels = append(labels, label)
			}
		}
	}
	return labels, nil
}

func findLabelColumn(header []string) (int, error) {
	h := csvutil.NewHeader(header)
	for _, name := range []string{"sci_name", "com_name", "name", "label"} {
		if idx := h.Col(name); idx >= 0 {
			return idx, nil
		}
	}
	// No known label column found; return error instead of guessing.
	return -1, fmt.Errorf("CSV has no recognized label column (expected one of: sci_name, com_name, name, label); found headers: %v", header)
}

func loadLabelsJSON(data []byte) ([]string, error) {
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		return arr, nil
	}

	var obj struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal(data, &obj); err == nil && obj.Labels != nil {
		return obj.Labels, nil
	}

	var named []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &named); err == nil && named != nil {
		labels := make([]string, len(named))
		for i, n := range named {
			labels[i] = n.Name
		}
		return labels, nil
	}

	return nil, &LabelLoadError{Path: "(json)", Reason: "unrecognized JSON label format"}
}
