package file

import (
	"github.com/spf13/cobra"
	"github.com/tphakala/BirdNET-Go/internal/analysis"
	"github.com/tphakala/BirdNET-Go/internal/config"
)

// FileCommand creates a new file command for analyzing a single audio file.
func Command(ctx *config.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file [input.wav]",
		Short: "Analyze an audio file",
		Long:  `Analyze a single audio file for bird calls and songs.`,
		Args:  cobra.ExactArgs(1), // the command expects exactly one argument
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx.Settings.InputFile = args[0]
			return analysis.FileAnalysis(ctx)
		},
	}

	// Set up flags specific to the 'file' command
	setupFlags(cmd, ctx.Settings)

	return cmd
}

// setupFileFlags configures flags specific to the file command.
func setupFlags(cmd *cobra.Command, settings *config.Settings) {
	cmd.Flags().StringVarP(&settings.OutputDir, "output", "o", "", "Path to output directory")
	cmd.Flags().StringVarP(&settings.OutputFormat, "format", "f", "", "Output format: table, csv")

	config.BindFlags(cmd, settings)
}
