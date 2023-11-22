package authors

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/config"
)

//go:embed AUTHORS
var authorsFile embed.FS

// Command creates a new cobra.Command to print authors.
func Command(cfg *config.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "authors",
		Short: "Print the list of authors",
		Long:  "Prints the contents of the authors.txt file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := fs.ReadFile(authorsFile, "AUTHORS")
			if err != nil {
				return err
			}
			fmt.Print(string(data))
			return nil
		},
	}

	return cmd
}
