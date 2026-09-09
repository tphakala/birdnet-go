package hwprofile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Board kinds reported in Board.Kind.
const (
	// BoardRaspberryPi marks a Raspberry Pi of any model.
	BoardRaspberryPi = "raspberry-pi"
	// BoardGeneric marks any host the device tree does not identify, which
	// includes every PC. There is deliberately no DMI fallback: DMI cannot
	// identify a Pi, and no model recommendation depends on knowing which
	// particular PC an x86 host is.
	BoardGeneric = "generic"
)

// Board tiers reported in Board.Tier. These are the performance bands the model
// catalog distinguishes, not a complete list of Raspberry Pi models.
const (
	TierPi5 = "pi5"
	TierPi4 = "pi4"
	TierPi3 = "pi3"
)

// Device-tree paths, relative to the filesystem root. The /proc mount is the
// usual location; /sys/firmware/devicetree/base is the same data exposed by the
// sysfs firmware node, present on kernels that do not mount the /proc alias.
const (
	dtModelPath         = "proc/device-tree/model"
	dtModelFallback     = "sys/firmware/devicetree/base/model"
	dtCompatPath        = "proc/device-tree/compatible"
	dtCompatFallback    = "sys/firmware/devicetree/base/compatible"
	raspberryPiCompat   = "raspberrypi,"
	raspberryPiModelKey = "raspberry pi"
)

// socTiers bands a system-on-chip into the performance tier the model catalog
// distinguishes. Keys are device-tree compatible parts, i.e. exactly the
// strings socFromCompatible produces, not die names or marketing names: a Pi 3
// reports "brcm,bcm2837", and the bcm2710 spelling appears only in downstream
// DTS filenames, which this never reads.
//
// bcm2835 and bcm2836 are absent because no current model runs on them. Note
// that a Pi 2 v1.2 and a Pi Zero 2 W both report bcm2837 and are therefore
// banded pi3, which is correct for the SoC even though the boards differ.
var socTiers = map[string]string{
	"bcm2712": TierPi5,
	"bcm2711": TierPi4,
	"bcm2837": TierPi3,
}

// detectBoard identifies the host board from the device tree under root. A host
// with no device tree is not a failure: that is every PC, and it yields
// BoardGeneric with an empty Tier and no issue.
func detectBoard(root string) (board Board, issues []Issue) {
	model, modelErr := readDeviceTreeString(root, dtModelPath, dtModelFallback)
	compatible, compatErr := readDeviceTreeList(root, dtCompatPath, dtCompatFallback)

	if modelErr != nil || compatErr != nil {
		issues = append(issues, Issue{Probe: ProbeBoard, Reason: ReasonReadFailed})
	}
	if model == "" && len(compatible) == 0 {
		return Board{Kind: BoardGeneric}, issues
	}

	board = Board{Kind: BoardGeneric, Model: model}
	board.SoC = socFromCompatible(compatible)
	board.Tier = socTiers[board.SoC]
	if isRaspberryPi(model, compatible) {
		board.Kind = BoardRaspberryPi
	}
	return board, issues
}

// socFromCompatible extracts the system-on-chip identifier from a device-tree
// compatible list. Entries are "vendor,part" strings ordered most specific
// first, e.g. ["raspberrypi,5-model-b", "brcm,bcm2712"]. A known SoC anywhere in
// the list wins; otherwise the last entry is used, since device-tree convention
// puts the most generic (the SoC family) last, so an unrecognised board still
// reports its SoC with an empty tier.
func socFromCompatible(compatible []string) string {
	if len(compatible) == 0 {
		return ""
	}
	var last string
	for _, entry := range compatible {
		part := compatiblePart(entry)
		if part == "" {
			continue
		}
		if _, known := socTiers[part]; known {
			return part
		}
		last = part
	}
	return last
}

// compatiblePart returns the part identifier of a device-tree compatible entry,
// i.e. what follows the vendor prefix. "brcm,bcm2712" yields "bcm2712".
func compatiblePart(entry string) string {
	entry = strings.ToLower(strings.TrimSpace(entry))
	if _, part, found := strings.Cut(entry, ","); found {
		return part
	}
	return entry
}

// isRaspberryPi reports whether the device tree identifies a Raspberry Pi,
// checking both the model string and the compatible vendor prefix so a board
// with an unexpected model string is still recognised.
func isRaspberryPi(model string, compatible []string) bool {
	if strings.Contains(strings.ToLower(model), raspberryPiModelKey) {
		return true
	}
	for _, entry := range compatible {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(entry)), raspberryPiCompat) {
			return true
		}
	}
	return false
}

// readDeviceTreeString reads a single-string device-tree property, trying the
// primary path then the fallback. Device-tree strings are NUL-terminated, so
// everything from the first NUL is dropped. A missing property is not an error:
// it just means this host has no device tree.
func readDeviceTreeString(root, primary, fallback string) (string, error) {
	data, err := readDeviceTreeFile(root, primary, fallback)
	if err != nil || len(data) == 0 {
		return "", err
	}
	value, _, _ := strings.Cut(string(data), "\x00")
	return strings.TrimSpace(value), nil
}

// readDeviceTreeList reads a string-list device-tree property. Entries are
// NUL-separated, and the property has a trailing NUL, which yields an empty
// final element that is dropped.
func readDeviceTreeList(root, primary, fallback string) ([]string, error) {
	data, err := readDeviceTreeFile(root, primary, fallback)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	parts := strings.Split(string(data), "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

// readDeviceTreeFile reads primary, falling back to fallback when primary does
// not exist. It returns a nil error when neither path exists, and a non-nil
// error only when a path is present but unreadable (a permission problem inside
// a hardened container), which is the case worth surfacing as an Issue.
func readDeviceTreeFile(root, primary, fallback string) ([]byte, error) {
	data, primaryErr := os.ReadFile(filepath.Join(root, primary))
	if primaryErr == nil {
		return data, nil
	}
	data, fallbackErr := os.ReadFile(filepath.Join(root, fallback))
	if fallbackErr == nil {
		return data, nil
	}
	if errors.Is(primaryErr, fs.ErrNotExist) && errors.Is(fallbackErr, fs.ErrNotExist) {
		return nil, nil
	}
	// Prefer the primary path's error: the fallback is usually absent on the
	// same hosts where the primary is readable.
	if !errors.Is(primaryErr, fs.ErrNotExist) {
		return nil, primaryErr
	}
	return nil, fallbackErr
}
