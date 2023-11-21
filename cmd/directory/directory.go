package directory

import (
	"github.com/spf13/cobra"
	"github.com/tphakala/BirdNET-Go/internal/analysis"
	"github.com/tphakala/BirdNET-Go/internal/config"
)

// DirectoryCommand creates a new cobra.Command for directory analysis.
func Command(ctx *config.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "directory [path]",
		Short: "Analyze all *.wav files in a directory",
		Long:  "Provide a directory path to analyze all *.wav files within it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The directory to analyze is passed as the first argument
			ctx.Settings.InputDirectory = args[0]
			return analysis.DirectoryAnalysis(ctx)
		},
	}

	setupFlags(cmd, ctx.Settings)

	return cmd
}

// setupDirectoryFlags defines flags specific to the directory command.
func setupFlags(cmd *cobra.Command, settings *config.Settings) {
	cmd.Flags().BoolVarP(&settings.Recursive, "recursive", "r", false, "Recursively analyze subdirectories")
	cmd.Flags().StringVarP(&settings.OutputDir, "output", "o", "", "Path to output directory")
	cmd.Flags().StringVarP(&settings.OutputFormat, "format", "f", "", "Output format: table, csv")

	// Bind flags to configuration
	config.BindFlags(cmd, settings)
}
