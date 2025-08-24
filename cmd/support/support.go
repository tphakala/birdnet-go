package support

import (
	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/conf"
	runtimectx "github.com/tphakala/birdnet-go/internal/runtime"
)

// Command creates the support parent command
func Command(settings *conf.Settings, runtime *runtimectx.Context) *cobra.Command {
	supportCmd := &cobra.Command{
		Use:   "support",
		Short: "Commands related to support operations in BirdNET-Go",
	}

	// Add subcommands here
	supportCmd.AddCommand(CollectCommand(runtime))

	return supportCmd
}
