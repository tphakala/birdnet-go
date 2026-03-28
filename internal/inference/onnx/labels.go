//go:build onnx

package onnx

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func loadLabels(path string) ([]string, error) {
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
	case ".txt":
		return loadLabelsText(data)
	case ".csv":
		return loadLabelsCSV(data)
	case ".json":
		return loadLabelsJSON(data)
	default:
		return nil, &LabelLoadError{Path: "(bytes)", Reason: "unsupported label file extension: " + ext}
	}
}

func loadLabelsText(data []byte) ([]string, error) {
	var labels []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
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
		return nil, nil
	}

	header := records[0]
	colIdx := findLabelColumn(header)

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

func findLabelColumn(header []string) int {
	priorities := []string{"sci_name", "com_name", "name", "label"}
	for _, name := range priorities {
		for i, h := range header {
			if strings.EqualFold(strings.TrimSpace(h), name) {
				return i
			}
		}
	}
	for i, h := range header {
		h = strings.TrimSpace(h)
		if _, err := strconv.Atoi(h); err != nil {
			return i
		}
	}
	return 0
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
