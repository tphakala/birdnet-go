package cpuspec

import (
	"regexp"
	"runtime"
	"strings"

	"github.com/klauspost/cpuid/v2"
)

// CPUSpec contains information about CPU specifications
type CPUSpec struct {
	BrandName        string
	PerformanceCores int
	EfficiencyCores  int
}

// GetCPUSpec returns CPU specifications including the number of performance cores
func GetCPUSpec() CPUSpec {
	brandName := cpuid.CPU.BrandName

	spec := CPUSpec{
		BrandName:        brandName,
		PerformanceCores: determinePerformanceCores(brandName),
	}

	return spec
}

// GetOptimalThreadCount returns the recommended number of threads for BirdNet analysis
func (c CPUSpec) GetOptimalThreadCount() int {
	// Get actual available CPU count (important for VMs)
	availableCPUs := runtime.NumCPU()

	// For hybrid architectures (with P and E cores), we primarily want to use Performance cores
	if c.PerformanceCores > 0 {
		recommendedThreads := c.PerformanceCores
		if recommendedThreads > availableCPUs {
			return availableCPUs
		}
		return recommendedThreads
	}

	// Fallback to using all logical cores if we can't determine P-cores
	return cpuid.CPU.LogicalCores
}

func determinePerformanceCores(brandName string) int {
	brandName = strings.ToLower(brandName)

	// Intel 12th, 13th, 14th gen and Core Ultra mapping
	intelCoreRegex := regexp.MustCompile(`intel.*(?:core.*i[357,9]-(\d{5})|core.*ultra\s+([579])\s+(?:processor\s+)?(\d{3}))`)
	if matches := intelCoreRegex.FindStringSubmatch(brandName); len(matches) > 1 {
		if matches[1] != "" { // Legacy Core i series
			model := matches[1]
			switch {
			case strings.HasPrefix(model, "127"): // 12th gen
				switch model {
				case "12900":
					return 8 // 12900K, 12900KS, 12900KF have 8 P-cores
				case "12700":
					return 8 // 12700K, 12700KF, 12700 have 8 P-cores
				case "12600":
					return 6 // 12600K, 12600KF have 6 P-cores
				case "12400":
					return 6 // 12400, 12400F have 6 P-cores
				case "12100":
					return 4 // 12100, 12100F have 4 P-cores
				}
			case strings.HasPrefix(model, "137"): // 13th gen
				switch model {
				case "13900":
					return 8 // 13900K, 13900KS, 13900KF, 13900 all have 8 P-cores
				case "13700":
					return 8 // 13700K, 13700KF, 13700 all have 8 P-cores
				case "13600":
					return 6 // 13600K, 13600KF have 6 P-cores
				case "13500":
					return 6 // 13500 has 6 P-cores
				case "13400":
					return 6 // 13400, 13400F have 6 P-cores
				case "13100":
					return 4 // 13100, 13100F have 4 P-cores
				}
			case strings.HasPrefix(model, "147"): // 14th gen
				switch model {
				case "14900":
					return 8 // 14900K, 14900KF have 8 P-cores
				case "14700":
					return 8 // 14700K, 14700KF have 8 P-cores
				case "14600":
					return 6 // 14600K, 14600KF have 6 P-cores
				case "14400":
					return 6 // 14400, 14400F have 6 P-cores
				case "14100":
					return 4 // 14100, 14100F have 4 P-cores
				}
			}
		} else if matches[2] != "" { // Core Ultra series
			series := matches[2]
			model := matches[3]
			switch {
			case series == "9":
				switch model { //nolint:gocritic // ignore gocritic warning for this switch statement
				case "285":
					return 8 // Core Ultra 9 285(K): 8 P-cores (not 16 - that was E-cores)
				}
			case series == "7":
				switch model {
				case "265", "265K", "265H":
					return 8 // Core Ultra 7 265: 8 P-cores (not 12 - that was E-cores)
				case "255":
					return 8 // Core Ultra 7 255: 8 P-cores
				}
			case series == "5":
				switch model {
				case "235":
					return 6 // Core Ultra 5 235: 6 P-cores
				case "225":
					return 4 // Core Ultra 5 225: 4 P-cores
				}
			}
		}
	}

	// Apple Silicon mapping
	appleRegex := regexp.MustCompile(`(?i)apple\s+(m[123,4]\s*(pro|max|ultra)?)\s*`)
	if matches := appleRegex.FindStringSubmatch(brandName); len(matches) > 1 {
		chip := strings.ToLower(strings.TrimSpace(matches[1]))
		switch chip {
		// M1 series
		case "m1":
			return 4 // Base M1: 4 performance cores
		case "m1 pro":
			return 8 // M1 Pro: 8 or 6 performance cores (we'll use higher value)
		case "m1 max":
			return 8 // M1 Max: 8 performance cores
		case "m1 ultra":
			return 16 // M1 Ultra: 16 performance cores (2x Max)
		// M2 series
		case "m2":
			return 4 // Base M2: 4 performance cores
		case "m2 pro":
			return 8 // M2 Pro: 8 or 6 performance cores (we'll use higher value)
		case "m2 max":
			return 12 // M2 Max: 12 performance cores
		case "m2 ultra":
			return 24 // M2 Ultra: 24 performance cores (2x Max)
		// M3 series
		case "m3":
			return 4 // Base M3: 4 performance cores
		case "m3 pro":
			return 8 // M3 Pro: 8 or 6 performance cores (we'll use higher value)
		case "m3 max":
			return 12 // M3 Max: 12 performance cores
		case "m3 ultra":
			return 24 // M3 Ultra: 24 performance cores (2x Max)
		// M4 series
		case "m4":
			return 6 // Base M4: 6 performance cores
		case "m4 pro":
			return 8 // M4 Pro: 8 performance cores
		case "m4 max":
			return 12 // M4 Max: 12 performance cores
		}
	}

	// If we can't determine P-cores, return 0
	return 0
}
