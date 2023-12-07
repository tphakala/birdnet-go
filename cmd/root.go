package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/cmd/authors"
	"github.com/tphakala/birdnet-go/cmd/directory"
	"github.com/tphakala/birdnet-go/cmd/file"
	"github.com/tphakala/birdnet-go/cmd/license"
	"github.com/tphakala/birdnet-go/cmd/realtime"
	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/pkg/birdnet"
)

// RootCommand creates and returns the root command
func RootCommand(ctx *config.Context) *cobra.Command {
	//ctx := config.GetGlobalContext()

	rootCmd := &cobra.Command{
		Use:   "birdnet",
		Short: "BirdNET-Go CLI",
	}

	// Set up the global flags for the root command.
	setupFlags(rootCmd, ctx.Settings)

	// Add sub-commands to the root command.
	fileCmd := file.Command(ctx)
	directoryCmd := directory.Command(ctx)
	realtimeCmd := realtime.Command(ctx)
	authorsCmd := authors.Command()
	licenseCmd := license.Command()

	subcommands := []*cobra.Command{
		fileCmd,
		directoryCmd,
		realtimeCmd,
		authorsCmd,
		licenseCmd,
	}

	rootCmd.AddCommand(subcommands...)

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Parse the command line flags
		if err := cmd.Flags().Parse(args); err != nil {
			return err
		}

		// Now sync the cfg struct with viper's values to ensure command-line arguments take precedence
		config.SyncViper(ctx.Settings)

		// Skip setup for authors and license commands
		if cmd.Name() != authorsCmd.Name() && cmd.Name() != licenseCmd.Name() {
			return initialize(ctx)
		}

		// Return nil to proceed without initializing for the excluded commands
		return nil
	}

	return rootCmd
}

// initialize is called before any subcommands are run, but after the context is ready
// This function is responsible for setting up configurations, ensuring the environment is ready, etc.
func initialize(ctx *config.Context) error {
	// Example of locale normalization and checking
	inputLocale := strings.ToLower(ctx.Settings.Locale)
	normalizedLocale, err := config.NormalizeLocale(inputLocale)
	if err != nil {
		return err
	}
	ctx.Settings.Locale = normalizedLocale

	// Initialize the BirdNET system with the normalized locale
	if err := birdnet.Setup(ctx); err != nil {
		return fmt.Errorf("failed to setup BirdNET: %w", err)
	}

	return nil
}

// defineGlobalFlags defines flags that are global to the command line interface
func setupFlags(rootCmd *cobra.Command, settings *config.Settings) error {
	rootCmd.PersistentFlags().BoolVarP(&settings.Debug, "debug", "d", viper.GetBool("debug"), "Enable debug output")
	rootCmd.PersistentFlags().StringVar(&settings.Locale, "locale", viper.GetString("node.locale"), "Set the locale for labels. Accepts full name or 2-letter code.")
	rootCmd.PersistentFlags().Float64VarP(&settings.Sensitivity, "sensitivity", "s", viper.GetFloat64("birdnet.sensitivity"), "Sigmoid sensitivity value between 0.0 and 1.5")
	rootCmd.PersistentFlags().Float64VarP(&settings.Threshold, "threshold", "t", viper.GetFloat64("birdnet.threshold"), "Confidency threshold for detections, value between 0.1 to 1.0")
	rootCmd.PersistentFlags().Float64Var(&settings.Overlap, "overlap", viper.GetFloat64("birdnet.overlap"), "Overlap value between 0.0 and 2.9")
	rootCmd.PersistentFlags().Float64Var(&settings.Latitude, "latitude", viper.GetFloat64("birdnet.latitude"), "Latitude for species prediction")
	rootCmd.PersistentFlags().Float64Var(&settings.Longitude, "longitude", viper.GetFloat64("birdnet.longitude"), "Longitude for species prediction")

	if err := viper.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		return fmt.Errorf("error binding flags: %v", err)
	}

	return nil
}
