package directory

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

// DirectoryCommand creates a new cobra.Command for directory analysis.
func Command(settings *conf.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "directory [path]",
		Short: "Analyze all *.wav files in a directory",
		Long:  "Provide a directory path to analyze all *.wav files within it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a context that can be cancelled
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Set up signal handling
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

			// Handle shutdown in a separate goroutine
			go func() {
				sig := <-sigChan
				fmt.Print("\n") // Add newline before the interrupt message
				fmt.Printf("Received signal %v, initiating graceful shutdown...\n", sig)
				cancel()
			}()

			// Ensure cleanup on exit
			defer func() {
				signal.Stop(sigChan)
			}()

			// The directory to analyze is passed as the first argument
			settings.Input.Path = args[0]
			err := analysis.DirectoryAnalysis(settings, ctx)
			if err != nil {
				if err == context.Canceled {
					return nil
				}
				return err
			}
			return nil
		},
	}

	// Disable printing usage on error
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	// Set up flags specific to the directory command
	if err := setupFlags(cmd, settings); err != nil {
		fmt.Printf("error setting up flags: %v\n", err)
		os.Exit(1)
	}

	return cmd
}

// setupDirectoryFlags defines flags specific to the directory command.
func setupFlags(cmd *cobra.Command, settings *conf.Settings) error {
	cmd.Flags().BoolVarP(&settings.Input.Recursive, "recursive", "r", false, "Recursively analyze subdirectories")
	cmd.Flags().BoolVarP(&settings.Input.Watch, "watch", "w", false, "Watch directory for new files")
	cmd.Flags().StringVarP(&settings.Output.File.Path, "output", "o", viper.GetString("output.file.path"), "Path to output directory")
	cmd.Flags().StringVar(&settings.Output.File.Type, "type", viper.GetString("output.file.type"), "Output type: table, csv")

	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %w", err)
	}

	return nil
}
