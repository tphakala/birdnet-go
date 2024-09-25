package support

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
)

// DiagnosticsCommand creates the diagnostics subcommand
func CollectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "collect",
		Short: "Collect system diagnostics for troubleshooting",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Collecting diagnostics...")
			zipFile, err := diagnostics.CollectDiagnostics()
			if err != nil {
				fmt.Printf("Error collecting diagnostics: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Diagnostics collected and saved to: %s\n", zipFile)
		},
	}
}
