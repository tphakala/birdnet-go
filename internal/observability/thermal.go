package observability

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultThermalBasePath is the base directory for Linux thermal zones. Every
// CPU-temperature consumer (the metrics collector, the /system/temperature/cpu
// endpoint, and the temperature health check) scans below this path.
const DefaultThermalBasePath = "/sys/class/thermal/"

// CPU temperature bounds and conversion for sysfs thermal-zone readings.
const (
	// milliCelsiusPerCelsius converts sysfs thermal readings, which are exposed
	// in milli-degrees Celsius, to degrees Celsius.
	milliCelsiusPerCelsius = 1000.0
	// minValidCPUTempCelsius and maxValidCPUTempCelsius bound plausible CPU
	// readings used to reject bogus sensor values. The upper bound is
	// deliberately generous: high-performance x86 CPUs can legitimately report
	// above 100°C under load before thermal throttling.
	minValidCPUTempCelsius = 0.0
	maxValidCPUTempCelsius = 120.0
)

// cpuThermalSensorTypes holds the sysfs thermal-zone "type" values that identify
// a CPU temperature sensor, filtering out unrelated zones (GPU, battery, ACPI).
var cpuThermalSensorTypes = map[string]bool{
	"cpu-thermal":     true, // Common on Raspberry Pi
	"x86_pkg_temp":    true, // Common on Intel x86 systems (like NUC)
	"soc_thermal":     true, // Common on some ARM SoCs
	"cpu_thermal":     true, // Alternative name
	"thermal-fan-est": true, // Seen on some systems
}

// ReadCPUTemperature scans Linux thermal zones under basePath for a CPU
// temperature sensor. It returns the hottest valid reading in Celsius, details
// about the selected sensor, and any error.
//
// It is the single source of truth for CPU thermal-zone reading, shared by the
// metrics collector, the /system/temperature/cpu API endpoint, and the
// temperature health check, so all three stay consistent on the sensor-type
// allowlist, conversion, and valid range.
//
// The hottest valid zone is selected rather than the first, so multi-zone
// systems (dual-socket, multi-die) still surface an overheating package and
// selection does not depend on glob ordering.
func ReadCPUTemperature(basePath string) (celsius float64, details string, err error) {
	zones, err := filepath.Glob(filepath.Join(basePath, "thermal_zone*"))
	if err != nil {
		return 0, "", fmt.Errorf("failed to scan for thermal zones: %w", err)
	}

	var (
		lastAttemptDetails string
		hottestCelsius     float64
		hottestDetails     string
		found              bool
	)
	for _, zonePath := range zones {
		zoneName := filepath.Base(zonePath)
		typePath := filepath.Join(zonePath, "type")

		//nolint:gosec // G304: typePath is from filepath.Glob
		typeData, err := os.ReadFile(typePath)
		if err != nil {
			continue
		}

		sensorType := strings.ToLower(strings.TrimSpace(string(typeData)))
		if !cpuThermalSensorTypes[sensorType] {
			continue
		}

		tempFilePath := filepath.Join(zonePath, "temp")
		//nolint:gosec // G304: tempFilePath is from filepath.Glob
		tempData, err := os.ReadFile(tempFilePath)
		if err != nil {
			lastAttemptDetails = fmt.Sprintf("Error reading temp from %s (type: %s)", zoneName, sensorType)
			continue
		}

		tempStr := strings.TrimSpace(string(tempData))
		tempMilliCelsius, err := strconv.Atoi(tempStr)
		if err != nil {
			lastAttemptDetails = fmt.Sprintf("Error parsing temp from %s (type: %s, value: '%s')", zoneName, sensorType, tempStr)
			continue
		}

		zoneCelsius := float64(tempMilliCelsius) / milliCelsiusPerCelsius
		if zoneCelsius < minValidCPUTempCelsius || zoneCelsius > maxValidCPUTempCelsius {
			lastAttemptDetails = fmt.Sprintf("Invalid temp from %s (type: %s, value: %.1f°C, expected %.0f-%.0f°C)", zoneName, sensorType, zoneCelsius, minValidCPUTempCelsius, maxValidCPUTempCelsius)
			continue
		}

		// Track the hottest valid CPU zone rather than returning the first, so
		// multi-zone systems (dual-socket, multi-die) still surface an
		// overheating package and selection does not depend on glob ordering.
		if !found || zoneCelsius > hottestCelsius {
			hottestCelsius = zoneCelsius
			hottestDetails = fmt.Sprintf("Source: %s, Type: %s", zoneName, sensorType)
			found = true
		}
	}

	if found {
		return hottestCelsius, hottestDetails, nil
	}

	if lastAttemptDetails != "" {
		return 0, lastAttemptDetails, fmt.Errorf("a targeted CPU sensor was found but could not be read successfully or value was invalid")
	}

	return 0, "", fmt.Errorf("no valid CPU temperature sensor found")
}
