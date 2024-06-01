package realtime

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/analysis"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// RealtimeCommand creates a new command for real-time audio analysis.
func Command(settings *conf.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "realtime",
		Short: "Analyze audio in realtime mode",
		Long:  "Start analyzing incoming audio data in real-time looking for bird calls.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return analysis.RealtimeAnalysis(settings)
		},
	}

	// Set up flags specific to the 'file' command
	if err := setupFlags(cmd, settings); err != nil {
		fmt.Printf("error setting up flags: %v\n", err)
		os.Exit(1)
	}

	return cmd
}

// setupRealtimeFlags configures flags specific to the realtime command.
func setupFlags(cmd *cobra.Command, settings *conf.Settings) error {
	cmd.Flags().StringVar(&settings.Realtime.Audio.Export.Path, "clippath", viper.GetString("realtime.audio.export.path"), "Path to save audio clips")
	cmd.Flags().StringVar(&settings.Realtime.Audio.Source, "source", viper.GetString("realtime.audio.source"), "Audio capture source (\"sysdefault\", \"USB Audio\", \":0,0\", etc.)")
	cmd.Flags().StringVar(&settings.Realtime.Log.Path, "logpath", viper.GetString("realtime.log.path"), "Path to save log files")
	cmd.Flags().BoolVar(&settings.Realtime.ProcessingTime, "processingtime", viper.GetBool("realtime.processingtime"), "Report processing time for each detection")
	cmd.Flags().StringSliceVar(&settings.Realtime.RTSP.Urls, "rtsp", viper.GetStringSlice("realtime.rtsp.urls"), "URL of RTSP audio stream to capture")
	cmd.Flags().StringVar(&settings.Realtime.RTSP.Transport, "rtsptransport", viper.GetString("realtime.rtsp.transport"), "RTSP transport (tcp/udp)")
	cmd.Flags().BoolVar(&settings.Realtime.Telemetry.Enabled, "telemetry", viper.GetBool("realtime.telemetry.enabled"), "Enable Prometheus telemetry endpoint")
	cmd.Flags().StringVar(&settings.Realtime.Telemetry.Listen, "listen", viper.GetString("realtime.telemetry.listen"), "Listen address and port of telemetry endpoint")

	// Bind flags to the viper settings
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %v", err)
	}

	return nil
}
