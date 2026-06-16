package analysis

import (
	"strings"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/mempolicy"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// ApplyMemoryPolicy detects the effective system memory (host RAM or cgroup cap)
// and applies the runtime memory policy for constrained systems: a soft
// GOMEMLIMIT backstop on the Go heap and a glibc malloc arena cap. It is gated by
// the lowmemory.mode override (auto/on/off) and must run before inference threads
// start so the arena cap takes effect. These are cheap backstops; profiling shows
// the dominant memory cost is loaded model weights, which is addressed separately
// by model gating and quantization (the RAM-reduction epic).
func ApplyMemoryPolicy(settings *conf.Settings) {
	log := GetLogger()
	mode := settings.LowMemory.GetMode()
	res := mempolicy.Configure(mode)
	d := res.Decision

	if !d.Active {
		log.Info("memory policy: low-memory controls inactive",
			logger.String("mode", mode),
			logger.String("reason", d.Reason))
		return
	}

	log.Info("memory policy: low-memory controls active",
		logger.String("mode", mode),
		logger.String("reason", d.Reason),
		logger.Int64("detected_ram_mb", d.TotalRAMBytes/(1024*1024)),
		logger.Bool("gomemlimit_applied", res.Applied.MemLimitApplied),
		logger.Int64("gomemlimit_mb", res.Applied.GoMemLimitBytes/(1024*1024)),
		logger.Bool("arena_cap_applied", res.Applied.ArenaApplied),
		logger.Int("arena_max", res.Applied.ArenaMax))
}

// InitializeMetrics initializes the Prometheus metrics manager.
func InitializeMetrics() (*observability.Metrics, error) {
	metrics, err := observability.NewMetrics()
	if err != nil {
		return nil, errors.New(err).
			Component("analysis.startup").
			Category(errors.CategorySystem).
			Context("operation", "initialize_metrics").
			Build()
	}
	return metrics, nil
}

// PrintSystemDetails prints system information and analyzer configuration.
func PrintSystemDetails(settings *conf.Settings) {
	log := GetLogger()

	// Get system details with gopsutil
	info, err := host.Info()
	if err != nil {
		log.Warn("failed to retrieve host info", logger.Error(err))
	}

	var hwModel string
	if conf.IsLinuxArm64() {
		hwModel = strings.TrimSpace(conf.GetBoardModel())
	} else {
		hwModel = "unknown"
	}

	// Log system details (guard against nil info from host.Info failure)
	if info != nil {
		log.Info("system details",
			logger.String("os", info.OS),
			logger.String("platform", info.Platform),
			logger.String("platform_version", info.PlatformVersion),
			logger.String("hardware", hwModel))
	}

	// Log the start of BirdNET-Go Analyzer in realtime mode and its configurations.
	log.Info("Starting analyzer in realtime mode",
		logger.Float64("threshold", settings.BirdNET.Threshold),
		logger.Float64("overlap", settings.BirdNET.Overlap),
		logger.Float64("sensitivity", settings.BirdNET.Sensitivity),
		logger.Int("interval", settings.Realtime.Interval))
}
