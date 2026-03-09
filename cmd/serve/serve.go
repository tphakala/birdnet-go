package serve

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/analysis"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Command creates the serve command which starts the BirdNET-Go server.
// The "realtime" command is registered as an alias for backward compatibility.
func Command(settings *conf.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "serve",
		Aliases: []string{"realtime"},
		Short:   "Start the BirdNET-Go server",
		Long: `Start the BirdNET-Go server for real-time bird sound identification.
This command initializes all subsystems (audio capture, BirdNET model,
web interface, database) and runs until interrupted.

The "realtime" command is an alias for backward compatibility.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			application := app.New()
			application.Register(app.NewLegacyService("birdnet-go", func(quit <-chan struct{}) error {
				return analysis.RealtimeAnalysisWithQuit(settings, quit)
			}))

			if err := application.Start(cmd.Context()); err != nil {
				return err
			}
			return application.Wait()
		},
	}

	// Set up flags (migrated from cmd/realtime)
	if err := setupFlags(cmd, settings); err != nil {
		fmt.Printf("error setting up flags: %v\n", err)
		os.Exit(1)
	}

	return cmd
}

// setupFlags configures flags specific to the serve command.
// These are the same flags previously on the realtime command.
func setupFlags(cmd *cobra.Command, settings *conf.Settings) error {
	cmd.Flags().StringVar(&settings.Realtime.Audio.Export.Path, "clippath", viper.GetString("realtime.audio.export.path"), "Path to save audio clips")
	cmd.Flags().StringVar(&settings.Realtime.Audio.Source, "source", viper.GetString("realtime.audio.source"), "Audio capture source (\"sysdefault\", \"USB Audio\", \":0,0\", etc.)")
	cmd.Flags().StringVar(&settings.Realtime.Log.Path, "logpath", viper.GetString("realtime.log.path"), "Path to save log files")
	cmd.Flags().BoolVar(&settings.Realtime.ProcessingTime, "processingtime", viper.GetBool("realtime.processingtime"), "Report processing time for each detection")
	cmd.Flags().StringSliceVar(&settings.Realtime.RTSP.URLs, "rtsp", viper.GetStringSlice("realtime.rtsp.urls"), "URL of RTSP audio stream to capture")
	cmd.Flags().StringVar(&settings.Realtime.RTSP.Transport, "rtsptransport", viper.GetString("realtime.rtsp.transport"), "RTSP transport (tcp/udp)")
	cmd.Flags().BoolVar(&settings.Realtime.Telemetry.Enabled, "telemetry", viper.GetBool("realtime.telemetry.enabled"), "Enable Prometheus telemetry endpoint")
	cmd.Flags().StringVar(&settings.Realtime.Telemetry.Listen, "listen", viper.GetString("realtime.telemetry.listen"), "Listen address and port of telemetry endpoint")

	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %w", err)
	}

	return nil
}
