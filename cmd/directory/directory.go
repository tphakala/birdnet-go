package directory

import (
	"fmt"
	"os"

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
			// The directory to analyze is passed as the first argument
			settings.Input.Path = args[0]
			return analysis.DirectoryAnalysis(settings)
		},
	}

	// Set up flags specific to the 'file' command
	if err := setupFlags(cmd, settings); err != nil {
		fmt.Printf("error setting up flags: %v\n", err)
		os.Exit(1)
	}

	return cmd
}

// setupDirectoryFlags defines flags specific to the directory command.
func setupFlags(cmd *cobra.Command, settings *conf.Settings) error {
	cmd.Flags().BoolVarP(&settings.Input.Recursive, "recursive", "r", false, "Recursively analyze subdirectories")
	cmd.Flags().StringVarP(&settings.Output.File.Path, "output", "o", viper.GetString("output.file.path"), "Path to output directory")
	cmd.Flags().StringVar(&settings.Output.File.Type, "type", viper.GetString("output.file.type"), "Output type: table, csv")

	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %w", err)
	}

	return nil
}
