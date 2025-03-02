package rangefilter

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// PrintCommand creates the print subcommand
func PrintCommand(settings *conf.Settings) *cobra.Command {
	printCmd := &cobra.Command{
		Use:   "print",
		Short: "Print BirdNET range filter results",
		Run: func(cmd *cobra.Command, args []string) {
			// Initialize a logger for the range filter
			rangeLogger := logger.Named("rangefilter")

			bn, err := birdnet.NewBirdNET(settings, rangeLogger)
			if err != nil {
				fmt.Printf("Error initializing BirdNET: %v\n", err)
				return
			}

			dateStr, _ := cmd.Flags().GetString("date")
			weekNum, _ := cmd.Flags().GetInt("week")
			week := float32(weekNum)

			if dateStr == "" && weekNum == 0 {
				dateStr = GetCurrentDateFormatted()
			} else {
				// Validate date if provided
				if dateStr != "" {
					layout := "2006-01-02" // ISO 8601 date format (YYYY-MM-DD)
					_, err := time.Parse(layout, dateStr)
					if err != nil {
						fmt.Printf("Invalid date format: %s\n", err)
						return
					}
				}

				// Validate week number if provided
				if weekNum < 0 || weekNum > 48 {
					fmt.Printf("Invalid week number: %d. Valid range is 1 to 48.\n", weekNum)
					return
				}
			}

			// Run the filter process
			bn.RunFilterProcess(dateStr, week)
		},
	}

	// Define flags for the print subcommand
	printCmd.Flags().Float64Var(&settings.BirdNET.Latitude, "latitude", settings.BirdNET.Latitude, "Latitude for range filter")
	printCmd.Flags().Float64Var(&settings.BirdNET.Longitude, "longitude", settings.BirdNET.Longitude, "Longitude for range filter")
	printCmd.Flags().Float32Var(&settings.BirdNET.RangeFilter.Threshold, "threshold", settings.BirdNET.RangeFilter.Threshold, "Threshold for range filter")
	printCmd.Flags().StringVar(&settings.BirdNET.RangeFilter.Model, "model", settings.BirdNET.RangeFilter.Model, "Model for range filter")
	printCmd.Flags().String("date", "", "Date for the range filter process in ISO 8601 format (YYYY-MM-DD)")
	printCmd.Flags().Int("week", 0, "Week number for the range filter process, values 1 to 48")

	return printCmd
}

// GetCurrentDateFormatted returns the current date formatted as a string in ISO 8601 format.
func GetCurrentDateFormatted() string {
	layout := "2006-01-02" // ISO 8601 date format (YYYY-MM-DD)
	return time.Now().Format(layout)
}
