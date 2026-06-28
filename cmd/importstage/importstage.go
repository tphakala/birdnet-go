// Package importstage provides the hidden `birdnet-go import-stage` subcommand:
// a narrow privileged primitive that copies a validated BirdNET-Pi database (and
// optional audio) into a BirdNET-Go-owned staging directory and chowns it to the
// service user. It is invoked through sudo by the import elevation ladder, never
// by interactive users, so it is marked Hidden. It performs no shell expansion:
// cobra parses --flag=value argv directly.
package importstage

import (
	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/imports/staging"
)

// Command builds the hidden import-stage subcommand.
func Command(_ *conf.Settings) *cobra.Command {
	var opts staging.Options
	cmd := &cobra.Command{
		Use:    "import-stage",
		Short:  "Stage a BirdNET-Pi database into a BirdNET-Go-owned directory (internal)",
		Hidden: true,
		// NoArgs rejects positional arguments. The ladder builds the sudo argv
		// itself in --flag=value form with no shell involved, so a value like
		// --src=-rf is passed to os/exec as one token and cobra binds it as the
		// value of --src; there is no flag-injection path to defend against here.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := staging.Stage(cmd.Context(), opts)
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Src, "src", "", "absolute path to source birds.db")
	cmd.Flags().StringVar(&opts.Audio, "audio", "", "absolute path to source audio directory (optional)")
	cmd.Flags().StringVar(&opts.Dst, "dst", "", "absolute path to an empty staging directory")
	cmd.Flags().IntVar(&opts.UID, "uid", -1, "service-user uid to chown staged files to")
	cmd.Flags().IntVar(&opts.GID, "gid", -1, "service-user gid to chown staged files to")
	_ = cmd.MarkFlagRequired("src")
	_ = cmd.MarkFlagRequired("dst")
	// uid/gid are required: lchown(2) treats -1 (the flag default) as "leave
	// unchanged", which would silently leave staged files root-owned and
	// unreadable by the service user while Stage still reported success.
	_ = cmd.MarkFlagRequired("uid")
	_ = cmd.MarkFlagRequired("gid")
	return cmd
}
