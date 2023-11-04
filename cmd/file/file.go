package file

import (
	"github.com/spf13/cobra"
	"github.com/tphakala/go-birdnet/pkg/analysis"
	"github.com/tphakala/go-birdnet/pkg/config"
)

// NewFileCommand creates a new file command for analyzing a single audio file.
func Command(cfg *config.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file [input.wav]",
		Short: "Analyze an audio file",
		Long:  `Analyze a single audio file for bird calls and songs.`,
		Args:  cobra.ExactArgs(1), // the command expects exactly one argument
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.InputFile = args[0]
			return analysis.FileAnalysis(cfg)
		},
	}

	// Set up flags specific to the 'file' command
	setupFlags(cmd, cfg)

	return cmd
}

// setupFileFlags configures flags specific to the file command.
func setupFlags(cmd *cobra.Command, cfg *config.Settings) {
	cmd.Flags().StringVarP(&cfg.OutputDir, "output", "o", "", "Path to output directory")
	cmd.Flags().StringVarP(&cfg.OutputFormat, "format", "f", "", "Output format: table, csv")

	config.BindFlags(cmd, cfg)
}
