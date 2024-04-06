package file

import (
	"fmt"
	"os"

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
			// Input file path is the first argument
			settings.Input.Path = args[0]
			return analysis.FileAnalysis(settings)
		},
	}

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
		return fmt.Errorf("error binding flags: %v", err)
	}

	return nil
}
