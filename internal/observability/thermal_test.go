package observability

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// thermalZone describes a fake /sys/class/thermal/thermal_zoneN entry.
type thermalZone struct {
	name string // e.g. "thermal_zone0"
	typ  string // sensor type, e.g. "cpu-thermal"
	temp string // raw milli-Celsius contents of the temp file, e.g. "45000"
}

// writeThermalZones materializes the given zones under a fresh temp dir and
// returns the base path to scan.
func writeThermalZones(t *testing.T, zones []thermalZone) string {
	t.Helper()
	base := t.TempDir()
	for _, z := range zones {
		dir := filepath.Join(base, z.name)
		require.NoError(t, os.Mkdir(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "type"), []byte(z.typ+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "temp"), []byte(z.temp+"\n"), 0o644))
	}
	return base
}

func TestReadCPUTemperature(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		zones       []thermalZone
		wantErr     bool
		wantCelsius float64
		// On success, wantDetails is the exact sensor details string. On error,
		// it is a substring expected in the returned details ("" asserts the
		// details are empty).
		wantDetails string
		// wantErrContains, on error cases, is a substring expected in err.Error()
		// so the two distinct error paths stay distinguishable.
		wantErrContains string
	}{
		{
			// Regression for issue #3940. filepath.Glob returns lexically
			// sorted zones, so the bogus non-CPU sensor (acpitz, 102.8°C) is
			// placed in thermal_zone0 and visited FIRST. The helper must skip
			// it on type and keep scanning to the real CPU sensor in
			// thermal_zone1, rather than reporting the non-CPU reading.
			name: "skips non-CPU sensor and finds later CPU zone",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "acpitz", temp: "102800"},
				{name: "thermal_zone1", typ: "cpu-thermal", temp: "45000"},
			},
			wantCelsius: 45.0,
			wantDetails: "Source: thermal_zone1, Type: cpu-thermal",
		},
		{
			// An unreadable/unparseable CPU zone visited first must not abort
			// the scan; a valid later CPU zone still wins.
			name: "skips unparseable CPU zone and finds later valid CPU zone",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "cpu-thermal", temp: "not-a-number"},
				{name: "thermal_zone1", typ: "cpu-thermal", temp: "45000"},
			},
			wantCelsius: 45.0,
			wantDetails: "Source: thermal_zone1, Type: cpu-thermal",
		},
		{
			// Multi-zone systems (dual-socket, multi-die) must report the
			// HOTTEST valid CPU zone, not the first or last in glob order, so an
			// overheating package is never masked by a cooler sibling. The
			// hottest zone here is neither first nor last.
			name: "reports hottest across multiple valid CPU zones",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "cpu-thermal", temp: "55000"},
				{name: "thermal_zone1", typ: "x86_pkg_temp", temp: "82000"},
				{name: "thermal_zone2", typ: "soc_thermal", temp: "60000"},
			},
			wantCelsius: 82.0,
			wantDetails: "Source: thermal_zone1, Type: x86_pkg_temp",
		},
		{
			// Regression for issue #4271. ARM big.LITTLE systems (such as
			// Orange Pi 4 Pro) expose cpul_thermal_zone and cpub_thermal_zone
			// alongside non-CPU zones (gpu, npu, ddr, skin). The helper must
			// filter out non-CPU zones and report the hottest CPU cluster.
			name: "recognizes Orange Pi 4 Pro ARM big.LITTLE thermal zones and reports hottest cluster",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "cpul_thermal_zone", temp: "62000"},
				{name: "thermal_zone1", typ: "cpub_thermal_zone", temp: "61938"},
				{name: "thermal_zone2", typ: "gpu_thermal_zone", temp: "70000"},
				{name: "thermal_zone3", typ: "npu_thermal_zone", temp: "60760"},
				{name: "thermal_zone4", typ: "ddr_thermal_zone", temp: "60264"},
				{name: "thermal_zone5", typ: "skin_zone", temp: "36400"},
			},
			wantCelsius: 62.0,
			wantDetails: "Source: thermal_zone0, Type: cpul_thermal_zone",
		},
		{
			// When the big cluster is hotter than the LITTLE cluster under load,
			// cpub_thermal_zone must be selected over cpul_thermal_zone.
			name: "selects cpub_thermal_zone when big cluster is hotter",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "cpul_thermal_zone", temp: "48000"},
				{name: "thermal_zone1", typ: "cpub_thermal_zone", temp: "65000"},
			},
			wantCelsius: 65.0,
			wantDetails: "Source: thermal_zone1, Type: cpub_thermal_zone",
		},
		{
			// A hot but plausible x86 package temperature above 100°C must be
			// accepted rather than rejected as out of range.
			name: "accepts high but valid x86 package temperature",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "x86_pkg_temp", temp: "105000"},
			},
			wantCelsius: 105.0,
			wantDetails: "Source: thermal_zone0, Type: x86_pkg_temp",
		},
		{
			// The inclusive upper boundary (120°C) must be accepted.
			name: "accepts upper boundary temperature",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "cpu-thermal", temp: "120000"},
			},
			wantCelsius: 120.0,
			wantDetails: "Source: thermal_zone0, Type: cpu-thermal",
		},
		{
			name: "no CPU sensor present",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "acpitz", temp: "50000"},
			},
			wantErr:         true,
			wantDetails:     "",
			wantErrContains: "no valid CPU temperature sensor found",
		},
		{
			name: "CPU temperature above valid range",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "x86_pkg_temp", temp: "121000"},
			},
			wantErr:         true,
			wantDetails:     "Invalid temp",
			wantErrContains: "could not be read successfully or value was invalid",
		},
		{
			name: "CPU temperature below valid range",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "cpu-thermal", temp: "-5000"},
			},
			wantErr:         true,
			wantDetails:     "Invalid temp",
			wantErrContains: "could not be read successfully or value was invalid",
		},
		{
			name: "unparseable CPU temperature",
			zones: []thermalZone{
				{name: "thermal_zone0", typ: "cpu-thermal", temp: "not-a-number"},
			},
			wantErr:         true,
			wantDetails:     "Error parsing temp",
			wantErrContains: "could not be read successfully or value was invalid",
		},
		{
			name:            "no thermal zones at all",
			zones:           nil,
			wantErr:         true,
			wantDetails:     "",
			wantErrContains: "no valid CPU temperature sensor found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base := writeThermalZones(t, tt.zones)
			celsius, details, err := ReadCPUTemperature(base)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
				if tt.wantDetails == "" {
					assert.Empty(t, details)
				} else {
					assert.Contains(t, details, tt.wantDetails)
				}
				return
			}

			require.NoError(t, err)
			assert.InEpsilon(t, tt.wantCelsius, celsius, 0.0001)
			assert.Equal(t, tt.wantDetails, details)
		})
	}
}
