// Package csvutil provides small shared helpers for parsing CSV data assets
// whose columns are addressed by header name rather than fixed position.
package csvutil

import "strings"

// Header resolves CSV column positions by name. It is built once from a header
// row and queried by name, matching case-insensitively and ignoring surrounding
// whitespace. Addressing columns by name (instead of fixed index) lets the
// underlying schema reorder columns or append new ones without breaking parsing,
// which is the common need across the project's CSV data assets.
//
// The zero value is usable: every lookup reports "not found".
type Header struct {
	index map[string]int
}

// NewHeader builds a Header from a CSV header row. On duplicate header names the
// first occurrence wins. Blank header cells are ignored.
func NewHeader(row []string) Header {
	index := make(map[string]int, len(row))
	for i, cell := range row {
		key := strings.ToLower(strings.TrimSpace(cell))
		if key == "" {
			continue
		}
		if _, exists := index[key]; !exists {
			index[key] = i
		}
	}
	return Header{index: index}
}

// Col returns the index of the named column, or -1 if it is absent.
func (h Header) Col(name string) int {
	if i, ok := h.index[strings.ToLower(strings.TrimSpace(name))]; ok {
		return i
	}
	return -1
}

// Field returns the value of the named column in rec, or "" if the column is
// absent from the header or missing from this (short) record.
func (h Header) Field(rec []string, name string) string {
	if i := h.Col(name); i >= 0 && i < len(rec) {
		return rec[i]
	}
	return ""
}
