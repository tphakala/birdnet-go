package onnx

import (
	"strconv"
	"strings"
)

// ortVersionMaxParts caps the dot-separated split ParseORTVersion makes:
// major, minor and the rest. Only major and minor are read.
const ortVersionMaxParts = 3

// ParseORTVersion extracts the major and minor numbers from an ONNX Runtime
// version string such as "1.25.1", as reported by ort.GetVersion.
//
// Surrounding spaces are trimmed. The string needs at least two dot-separated
// parts. The major part must be all decimal digits. The minor part is its
// leading run of decimal digits (at least one), so "1.27.0-rc1" and "1.29rc1"
// parse as 1.27 and 1.29. Strings such as "", "1", "abc", "1.x" and "v1.31.0"
// are not parseable and report ok == false.
func ParseORTVersion(v string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(v), ".", ortVersionMaxParts)
	if len(parts) < 2 {
		return 0, 0, false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return 0, 0, false
	}

	digits := parts[1]
	end := 0
	for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(digits[:end])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}
