package directory

import (
	"github.com/spf13/cobra"
	"github.com/tphakala/go-birdnet/pkg/analysis"
	"github.com/tphakala/go-birdnet/pkg/config"
)

// DirectoryCommand creates a new cobra.Command for directory analysis.
func Command(cfg *config.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "directory [path]",
		Short: "Analyze all *.wav files in a directory",
		Long:  "Provide a directory path to analyze all *.wav files within it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The directory to analyze is passed as the first argument
			cfg.InputDirectory = args[0]
			return analysis.DirectoryAnalysis(cfg)
		},
	}

	setupFlags(cmd, cfg)

	return cmd
}

// setupDirectoryFlags defines flags specific to the directory command.
func setupFlags(cmd *cobra.Command, cfg *config.Settings) {
	cmd.Flags().BoolVarP(&cfg.Recursive, "recursive", "r", false, "Recursively analyze subdirectories")
	cmd.Flags().StringVarP(&cfg.OutputDir, "output", "o", "", "Path to output directory")
	cmd.Flags().StringVarP(&cfg.OutputFormat, "format", "f", "", "Output format: table, csv")

	// Bind flags to configuration
	config.BindFlags(cmd, cfg)
}
