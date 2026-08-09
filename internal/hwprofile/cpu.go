package hwprofile

import (
	"runtime"

	"github.com/klauspost/cpuid/v2"
	gopsutilcpu "github.com/shirou/gopsutil/v3/cpu"
	xcpu "golang.org/x/sys/cpu"
)

// SIMD extension names reported in Profile.SIMD. Only the extensions that
// change which inference kernels a model build can use are reported; the full
// feature set is not the point here.
const (
	SIMDAVX2   = "avx2"
	SIMDAVX512 = "avx512"
	SIMDNEON   = "neon"
	SIMDSVE    = "sve"
)

// detectSIMD lists the SIMD extensions available on the running CPU. It reads
// the feature bits the process already has (klauspost/cpuid on x86,
// golang.org/x/sys/cpu HWCAP on ARM), so unlike the filesystem probes it is not
// root-parameterized: it always describes the CPU this process runs on.
func detectSIMD() []string {
	simd := make([]string, 0, 2)
	switch runtime.GOARCH {
	case archAMD64, arch386:
		if cpuid.CPU.Has(cpuid.AVX2) {
			simd = append(simd, SIMDAVX2)
		}
		if cpuid.CPU.Has(cpuid.AVX512F) {
			simd = append(simd, SIMDAVX512)
		}
	case archARM64:
		if xcpu.ARM64.HasASIMD {
			simd = append(simd, SIMDNEON)
		}
		if xcpu.ARM64.HasSVE {
			simd = append(simd, SIMDSVE)
		}
	case archARM:
		if xcpu.ARM.HasNEON {
			simd = append(simd, SIMDNEON)
		}
	}
	if len(simd) == 0 {
		return nil
	}
	return simd
}

// physicalCores returns the physical core count, falling back to the logical
// count when the physical count cannot be determined. It never returns 0: a
// core count of zero would read as "no CPU" in the panel, and the logical count
// is always a defensible answer.
func physicalCores() int {
	if n, err := gopsutilcpu.Counts(false); err == nil && n > 0 {
		return n
	}
	return runtime.NumCPU()
}
