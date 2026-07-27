package hwprofile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectBoard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tree  map[string]string
		board Board
	}{
		{
			name:  "raspberry pi 5",
			tree:  fixturePi5,
			board: Board{Kind: BoardRaspberryPi, Model: "Raspberry Pi 5 Model B Rev 1.0", SoC: "bcm2712", Tier: TierPi5},
		},
		{
			name:  "raspberry pi 4",
			tree:  fixturePi4,
			board: Board{Kind: BoardRaspberryPi, Model: "Raspberry Pi 4 Model B Rev 1.4", SoC: "bcm2711", Tier: TierPi4},
		},
		{
			name:  "raspberry pi 3",
			tree:  fixturePi3,
			board: Board{Kind: BoardRaspberryPi, Model: "Raspberry Pi 3 Model B Plus Rev 1.3", SoC: "bcm2837", Tier: TierPi3},
		},
		{
			name:  "device tree exposed only through sysfs firmware node",
			tree:  fixturePi5SysfsOnly,
			board: Board{Kind: BoardRaspberryPi, Model: "Raspberry Pi 5 Model B Rev 1.0", SoC: "bcm2712", Tier: TierPi5},
		},
		{
			name:  "host without a device tree",
			tree:  nil,
			board: Board{Kind: BoardGeneric},
		},
		{
			name:  "generic amd64 host has no device tree even with a GPU",
			tree:  fixtureAMD64Desktop,
			board: Board{Kind: BoardGeneric},
		},
		{
			// An unrecognised board still reports its SoC so a support dump
			// carries the fact; only the tier is left empty.
			name: "unknown board reports soc without a tier",
			tree: map[string]string{
				"proc/device-tree/model":      "Radxa ROCK 5B\x00",
				"proc/device-tree/compatible": "radxa,rock-5b\x00rockchip,rk3588\x00",
			},
			board: Board{Kind: BoardGeneric, Model: "Radxa ROCK 5B", SoC: "rk3588"},
		},
		{
			// Pi 1 and Pi 2 are recognised as Raspberry Pis but have no tier:
			// no current model runs on them, so there is nothing to recommend.
			name: "raspberry pi below the supported tiers",
			tree: map[string]string{
				"proc/device-tree/model":      "Raspberry Pi 2 Model B Rev 1.1\x00",
				"proc/device-tree/compatible": "raspberrypi,2-model-b\x00brcm,bcm2836\x00",
			},
			board: Board{Kind: BoardRaspberryPi, Model: "Raspberry Pi 2 Model B Rev 1.1", SoC: "bcm2836"},
		},
		{
			// The model string alone identifies the vendor when the compatible
			// list carries no vendor prefix.
			name: "model string alone identifies a raspberry pi",
			tree: map[string]string{
				"proc/device-tree/model": "Raspberry Pi Compute Module 4 Rev 1.0\x00",
			},
			board: Board{Kind: BoardRaspberryPi, Model: "Raspberry Pi Compute Module 4 Rev 1.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := writeTree(t, tt.tree)

			board, issues := detectBoard(root)

			assert.Equal(t, tt.board, board)
			assert.Empty(t, issues, "a readable or absent device tree must not record an issue")
		})
	}
}

func TestSocFromCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		compatible []string
		want       string
	}{
		{
			name:       "known soc wins over position",
			compatible: []string{"raspberrypi,5-model-b", "brcm,bcm2712"},
			want:       "bcm2712",
		},
		{
			name: "known soc is picked even when it is not last",
			// Some device trees append an extra generic entry after the SoC.
			compatible: []string{"raspberrypi,4-model-b", "brcm,bcm2711", "brcm,bcm2835"},
			want:       "bcm2711",
		},
		{
			name:       "unknown list falls back to the most generic entry",
			compatible: []string{"radxa,rock-5b", "rockchip,rk3588"},
			want:       "rk3588",
		},
		{
			name:       "entry without a vendor prefix is used verbatim",
			compatible: []string{"bcm2712"},
			want:       "bcm2712",
		},
		{
			name:       "empty list yields no soc",
			compatible: nil,
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, socFromCompatible(tt.compatible))
		})
	}
}

func TestReadDeviceTreeList(t *testing.T) {
	t.Parallel()

	root := writeTree(t, fixturePi5)

	entries, err := readDeviceTreeList(root, dtCompatPath, dtCompatFallback)

	require.NoError(t, err)
	// The trailing NUL must not produce an empty final entry.
	assert.Equal(t, []string{"raspberrypi,5-model-b", "brcm,bcm2712"}, entries)
}

func TestReadDeviceTreeStringStopsAtNul(t *testing.T) {
	t.Parallel()

	root := writeTree(t, fixturePi5)

	model, err := readDeviceTreeString(root, dtModelPath, dtModelFallback)

	require.NoError(t, err)
	assert.Equal(t, "Raspberry Pi 5 Model B Rev 1.0", model)
}

func TestDetectBoardRecordsIssueWhenDeviceTreeIsUnreadable(t *testing.T) {
	t.Parallel()

	skipIfPermissionBitsIneffective(t)

	root := writeTree(t, map[string]string{
		"proc/device-tree/model": "Raspberry Pi 5 Model B Rev 1.0\x00",
	})
	makeUnreadable(t, root, dtModelPath)

	board, issues := detectBoard(root)

	// A device tree that is present but unreadable is the one case worth
	// distinguishing from "this host has no device tree".
	assert.Equal(t, Board{Kind: BoardGeneric}, board)
	assert.Equal(t, []Issue{{Probe: ProbeBoard, Reason: ReasonReadFailed}}, issues)
}
