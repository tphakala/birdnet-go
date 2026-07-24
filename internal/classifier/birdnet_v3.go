// birdnet_v3.go provides BirdNET v3.0 acoustic classifier label parsing.
// Label parsing is available in all builds; ONNX inference is in birdnet_v3_onnx.go.
package classifier

import (
	"bufio"
	"bytes"
	"strings"
)

// utf8BOM is the UTF-8 byte order mark that some label files carry on the first
// line. It is stripped so the first label is not corrupted. It is written as an
// escape (not a literal BOM) because a literal BOM is only legal at the very
// start of a Go source file.
const utf8BOM = "\uFEFF"

// Label scanner buffer sizes. Labels are short ("SciName_CommonName"), but the
// buffer is grown beyond bufio's default 64 KiB line cap defensively.
const (
	labelScannerInitialBufBytes = 64 * 1024   // initial scan buffer (64 KiB)
	labelScannerMaxLineBytes    = 1024 * 1024 // max label line length (1 MiB)
)

// ParseBirdNETV3Labels parses a BirdNET v3.0 label file.
//
// Format: one label per line in "Scientific name_Common name" form (the same
// format as BirdNET v2.4), one line per class in model-output order. Empty lines
// are skipped and a UTF-8 BOM on the first line is stripped. Every non-empty line
// is kept as a label; no header line is skipped, so the returned count equals the
// number of classes and a stray header would surface as a label-count mismatch at
// model load rather than silently shifting labels off by one.
func ParseBirdNETV3Labels(data []byte) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, labelScannerInitialBufBytes), labelScannerMaxLineBytes)

	var labels []string
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, utf8BOM)
			first = false
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		labels = append(labels, line)
	}

	return labels, scanner.Err()
}
