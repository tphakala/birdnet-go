// print.go range print command code
package rangefilter

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// PrintCommand creates the print subcommand
func PrintCommand(settings *conf.Settings) *cobra.Command {
	printCmd := &cobra.Command{
		Use:   "print",
		Short: "Print BirdNET range filter results",
		Run: func(cmd *cobra.Command, args []string) {
			bn, err := birdnet.NewBirdNET(settings)
			if err != nil {
				fmt.Printf("Error initializing BirdNET: %v\n", err)
				return
			}

			dateFormat := "eu"
			dateStr := GetCurrentDateFormatted(dateFormat)

			// Run the filter process
			bn.RunFilterProcess(dateStr, dateFormat)
		},
	}

	// Define flags for the print subcommand
	printCmd.Flags().Float64Var(&settings.BirdNET.Latitude, "latitude", settings.BirdNET.Latitude, "Latitude for range filter")
	printCmd.Flags().Float64Var(&settings.BirdNET.Longitude, "longitude", settings.BirdNET.Longitude, "Longitude for range filter")
	printCmd.Flags().Float32Var(&settings.BirdNET.RangeFilter.Threshold, "threshold", settings.BirdNET.RangeFilter.Threshold, "Threshold for range filter")
	printCmd.Flags().StringVar(&settings.BirdNET.RangeFilter.Model, "model", settings.BirdNET.RangeFilter.Model, "Model for range filter")

	return printCmd
}

// GetCurrentDateFormatted returns the current date formatted as a string based on the given format ("eu" for European and "us" for US).
func GetCurrentDateFormatted(dateFormat string) string {
	layout := "02/01/2006" // Default to European date format (DD/MM/YYYY)
	if dateFormat == "us" {
		layout = "01/02/2006" // US date format (MM/DD/YYYY)
	}

	return time.Now().Format(layout)
}
