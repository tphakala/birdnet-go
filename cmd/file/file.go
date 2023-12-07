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
			// Input file path is the first argument
			ctx.Settings.Input.Path = args[0]
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

	cmd.Flags().StringVarP(&settings.Output.File.Path, "output", "o", viper.GetString("output.file.path"), "Path to output directory")
	cmd.Flags().StringVar(&settings.Output.File.Type, "type", viper.GetString("output.file.type"), "Output type: table, csv")

	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %v", err)
	}

	return nil
}
