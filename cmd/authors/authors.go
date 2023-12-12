package authors

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"
)

//go:embed AUTHORS
var authorsFile embed.FS

// Command creates a new cobra.Command to print authors.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "authors",
		Short: "Print authors of BirdNET-Go",
		Long:  "",
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
