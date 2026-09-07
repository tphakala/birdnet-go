package support

import (
	"github.com/spf13/cobra"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// OpenVINOProbeCommand creates the hidden `support openvino-probe` subcommand.
// It is the child half of the out-of-process OpenVINO device probe
// (inference.OpenVINOProbeDevices): it loads the OpenVINO core in this
// process, enumerates the available devices, and reports them on stdout as
// marker-prefixed lines the parent parses. A crash anywhere in the vendor
// driver stack during enumeration then kills only this child, and the parent
// falls back to ONNX Runtime instead of crash-looping (issue #4236). The
// command is hidden because it is an internal protocol, not a user surface.
func OpenVINOProbeCommand(settings *conf.Settings) *cobra.Command {
	var libraryPath string
	cmd := &cobra.Command{
		Use:    "openvino-probe",
		Short:  "Internal: enumerate OpenVINO devices in an isolated process",
		Hidden: true,
		// Errors exit non-zero; the parent treats that (and any signal) as
		// "no OpenVINO devices". Usage help on failure would only pollute the
		// parsed stdout stream.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := libraryPath
			if path == "" && settings != nil {
				path = settings.BirdNET.OpenVINOPath
			}
			return inference.RunOVProbeChild(path, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&libraryPath, "library-path", "",
		"Path to libopenvino_c (defaults to birdnet.openvinopath from the config)")
	return cmd
}
