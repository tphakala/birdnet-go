package file

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/analysis"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// FileCommand creates a new file command for analyzing a single audio file.
func Command(settings *conf.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file [input.wav]",
		Short: "Analyze an audio file",
		Long:  `Analyze a single audio file for bird calls and songs.`,
		Args:  cobra.ExactArgs(1), // the command expects exactly one argument
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a context that can be cancelled
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Set up signal handling
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

			// Handle shutdown in a separate goroutine
			go func() {
				sig := <-sigChan
				fmt.Print("\n") // Add newline before the interrupt message
				fmt.Printf("Received signal %v, initiating graceful shutdown...", sig)
				cancel()
			}()

			// Input file path is the first argument
			settings.Input.Path = args[0]
			err := analysis.FileAnalysis(settings, ctx)
			if err == context.Canceled {
				// Return nil for user-initiated cancellation
				return nil
			}
			return err
		},
	}

	// Disable printing usage on error
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	// Set up flags specific to the 'file' command
	if err := setupFlags(cmd, settings); err != nil {
		fmt.Printf("error setting up flags: %v\n", err)
		os.Exit(1)
	}

	return cmd
}

// setupFileFlags configures flags specific to the file command.
func setupFlags(cmd *cobra.Command, settings *conf.Settings) error {

	cmd.Flags().StringVarP(&settings.Output.File.Path, "output", "o", viper.GetString("output.file.path"), "Path to output directory")
	cmd.Flags().StringVar(&settings.Output.File.Type, "type", viper.GetString("output.file.type"), "Output type: table, csv")

	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %w", err)
	}

	return nil
}
