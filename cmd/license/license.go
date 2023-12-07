package license

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"
)

//go:embed LICENSE
var authorsFile embed.FS

// Command creates a new cobra.Command to print authors.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "Print the license of Go-BirdNET",
		Long:  "",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := fs.ReadFile(authorsFile, "LICENSE")
			if err != nil {
				return err
			}
			fmt.Print("\n" + string(data) + "\n\n")
			return nil

		},
	}

	return cmd
}
