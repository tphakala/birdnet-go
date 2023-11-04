package realtime

import (
	"github.com/spf13/cobra"
	"github.com/tphakala/go-birdnet/pkg/analysis"
	"github.com/tphakala/go-birdnet/pkg/config"
)

// NewRealtimeCommand creates a new command for real-time audio analysis.
func Command(cfg *config.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "realtime",
		Short: "Analyze audio in realtime mode",
		Long:  "Start analyzing incoming audio data in real-time looking for bird calls.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return analysis.RealtimeAnalysis(cfg)
		},
	}

	setupFlags(cmd, cfg)

	return cmd
}

// setupRealtimeFlags configures flags specific to the realtime command.
func setupFlags(cmd *cobra.Command, cfg *config.Settings) {
	cmd.Flags().StringVar(&cfg.CapturePath, "savepath", "", "Path to save audio data")
	cmd.Flags().StringVar(&cfg.LogPath, "logpath", "", "Path to save log files")
	cmd.Flags().StringVar(&cfg.LogFile, "logfile", "", "Filename for the log file")
	cmd.Flags().BoolVar(&cfg.ProcessingTime, "processingtime", false, "Report processing time for each detection")

	config.BindFlags(cmd, cfg)
}
