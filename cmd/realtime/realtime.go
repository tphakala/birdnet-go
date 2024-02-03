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
func Command(ctx *conf.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "realtime",
		Short: "Analyze audio in realtime mode",
		Long:  "Start analyzing incoming audio data in real-time looking for bird calls.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return analysis.RealtimeAnalysis(ctx)
		},
	}

	// Set up flags specific to the 'file' command
	if err := setupFlags(cmd, ctx.Settings); err != nil {
		fmt.Printf("error setting up flags: %v\n", err)
		os.Exit(1)
	}

	return cmd
}

// setupRealtimeFlags configures flags specific to the realtime command.
func setupFlags(cmd *cobra.Command, settings *conf.Settings) error {
	cmd.Flags().StringVar(&settings.Realtime.AudioExport.Path, "clippath", viper.GetString("realtime.audioexport.path"), "Path to save audio clips")
	cmd.Flags().StringVar(&settings.Realtime.Log.Path, "logpath", viper.GetString("realtime.log.path"), "Path to save log files")
	cmd.Flags().BoolVar(&settings.Realtime.ProcessingTime, "processingtime", viper.GetBool("realtime.processingtime"), "Report processing time for each detection")

	// Bind flags to the viper settings
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %v", err)
	}

	return nil
}
