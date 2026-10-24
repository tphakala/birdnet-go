package onnx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseORTVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantMajor int
		wantMinor int
		wantOK    bool
	}{
		{name: "release", input: "1.25.1", wantMajor: 1, wantMinor: 25, wantOK: true},
		{name: "two parts", input: "1.27", wantMajor: 1, wantMinor: 27, wantOK: true},
		{name: "release candidate suffix on patch", input: "1.27.0-rc1", wantMajor: 1, wantMinor: 27, wantOK: true},
		{name: "suffix on minor", input: "1.29rc1", wantMajor: 1, wantMinor: 29, wantOK: true},
		{name: "surrounding spaces", input: " 1.31.0 ", wantMajor: 1, wantMinor: 31, wantOK: true},
		{name: "three digit minor", input: "1.100.0", wantMajor: 1, wantMinor: 100, wantOK: true},
		{name: "major 2", input: "2.0.0", wantMajor: 2, wantMinor: 0, wantOK: true},
		{name: "empty", input: ""},
		{name: "major only", input: "1"},
		{name: "letters", input: "abc"},
		{name: "non-numeric minor", input: "1.x"},
		{name: "v prefix", input: "v1.31.0"},
		{name: "lone dot", input: "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			major, minor, ok := ParseORTVersion(tt.input)
			assert.Equal(t, tt.wantOK, ok, "ok for %q", tt.input)
			assert.Equal(t, tt.wantMajor, major, "major for %q", tt.input)
			assert.Equal(t, tt.wantMinor, minor, "minor for %q", tt.input)
		})
	}
}
