package file

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/analysis"
	"github.com/tphakala/birdnet-go/internal/config"
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
	if err := setupFlags(cmd, ctx.Settings); err != nil {
		fmt.Printf("error setting up flags: %v\n", err)
		os.Exit(1)
	}

	return cmd
}

// setupFileFlags configures flags specific to the file command.
func setupFlags(cmd *cobra.Command, settings *config.Settings) error {
	cmd.Flags().StringVarP(&settings.OutputDir, "output", "o", "", "Path to output directory")
	cmd.Flags().StringVarP(&settings.OutputFormat, "format", "f", "", "Output format: table, csv")

	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %v", err)
	}

	return nil
}
