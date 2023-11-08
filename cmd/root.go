package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tphakala/go-birdnet/cmd/authors"
	"github.com/tphakala/go-birdnet/cmd/directory"
	"github.com/tphakala/go-birdnet/cmd/file"
	"github.com/tphakala/go-birdnet/cmd/license"
	"github.com/tphakala/go-birdnet/cmd/realtime"
	"github.com/tphakala/go-birdnet/pkg/birdnet"
	"github.com/tphakala/go-birdnet/pkg/config"
)

// RootCommand creates and returns the root command
func RootCommand(ctx *config.Context) *cobra.Command {
	//ctx := config.GetGlobalContext()

	rootCmd := &cobra.Command{
		Use:   "birdnet",
		Short: "Go-BirdNET CLI",
	}

	// Set up the global flags for the root command.
	defineGlobalFlags(rootCmd, ctx.Settings)

	// Add sub-commands to the root command.
	fileCmd := file.Command(ctx)
	directoryCmd := directory.Command(ctx)
	realtimeCmd := realtime.Command(ctx)
	authorsCmd := authors.Command(ctx.Settings)
	licenseCmd := license.Command(ctx.Settings)

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
	if err := birdnet.Setup(ctx.Settings); err != nil {
		return fmt.Errorf("failed to setup BirdNET: %w", err)
	}

	return nil
}

// defineGlobalFlags defines flags that are global to the command line interface
func defineGlobalFlags(rootCmd *cobra.Command, settings *config.Settings) {
	rootCmd.PersistentFlags().BoolP("debug", "d", viper.GetBool("debug"), "Enable debug output")
	rootCmd.PersistentFlags().Float64("sensitivity", viper.GetFloat64("sensitivity"), "Sigmoid sensitivity value between 0.0 and 1.5")
	rootCmd.PersistentFlags().Float64("overlap", viper.GetFloat64("overlap"), "Overlap value between 0.0 and 2.9")
	rootCmd.PersistentFlags().String("locale", viper.GetString("locale"), "Set the locale for labels. Accepts full name or 2-letter code.")
	rootCmd.PersistentFlags().Float64("threshold", viper.GetFloat64("threshold"), "Confidency threshold for detections, value between 0.1 to 1.0")

	// Binding the configuration flags to the settings
	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
	viper.BindPFlag("sensitivity", rootCmd.PersistentFlags().Lookup("sensitivity"))
	viper.BindPFlag("overlap", rootCmd.PersistentFlags().Lookup("overlap"))
	viper.BindPFlag("locale", rootCmd.PersistentFlags().Lookup("locale"))
	viper.BindPFlag("threshold", rootCmd.PersistentFlags().Lookup("threshold"))
}
