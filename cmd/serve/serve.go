package serve

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/analysis"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/events"
)

// Command creates the serve command which starts the BirdNET-Go server.
// The "realtime" command is registered as an alias for backward compatibility.
func Command(settings *conf.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "serve",
		Aliases: []string{"realtime"},
		Short:   "Start the BirdNET-Go server",
		Long: `Start the BirdNET-Go server for real-time bird sound identification.
This command initializes all subsystems (audio capture, BirdNET model,
web interface, database) and runs until interrupted.

The "realtime" command is an alias for backward compatibility.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Apply the runtime memory policy before any inference threads start,
			// so the glibc arena cap takes effect. Gated by lowmemory.mode.
			analysis.ApplyMemoryPolicy(settings)

			// Print system details early, before any service starts.
			analysis.PrintSystemDetails(settings)

			// Initialize metrics before services that depend on them.
			metrics, err := analysis.InitializeMetrics()
			if err != nil {
				return err
			}

			// Create the AudioEngine with a nil scheduler; the scheduler
			// depends on SunCalc and ControlChan that are only available
			// after APIServerService.Start(), so it is set later via
			// AudioEngine.SetScheduler().
			audioEngine := engine.New(cmd.Context(), &engine.Config{
				FFmpegPath:           settings.Realtime.Audio.FfmpegPath,
				SoxPath:              settings.Realtime.Audio.SoxPath,
				Transport:            settings.Realtime.RTSP.Transport,
				FFmpegParameters:     settings.Realtime.RTSP.FFmpegParameters,
				Debug:                settings.Debug,
				CaptureBufferSeconds: settings.Realtime.ExtendedCapture.EffectiveCaptureBufferSeconds(settings.Realtime.Audio.Export.PreCapture),
			}, nil)
			defer audioEngine.Stop()

			// Create services. Registration order determines start order;
			// shutdown happens in reverse within each tier.
			bnAnalyzer := analysis.NewBirdNETAnalyzer(settings)
			dbService := analysis.NewDatabaseService(settings, metrics)
			apiService := analysis.NewAPIServerService(settings, bnAnalyzer, dbService, metrics, audioEngine)
			audioService := analysis.NewAudioPipelineService(settings, bnAnalyzer, dbService, apiService, audioEngine)

			application := app.New()
			app.SetGlobal(application)
			application.Register(bnAnalyzer, dbService, apiService, audioService)

			if err := application.Start(cmd.Context()); err != nil {
				return err
			}

			// Wire the global event emitter after all services have started
			// (DatabaseService must be running so its datastore is available).
			if ds := dbService.DataStore(); ds != nil {
				events.SetDefault(events.NewEmitter(ds))
				emitStartupEvents(cmd.Context(), settings, ds)
			}

			return application.Wait()
		},
	}

	// Set up flags (migrated from cmd/realtime)
	if err := setupFlags(cmd, settings); err != nil {
		fmt.Printf("error setting up flags: %v\n", err)
		os.Exit(1)
	}

	return cmd
}

// setupFlags configures flags specific to the serve command.
// These are the same flags previously on the realtime command.
func setupFlags(cmd *cobra.Command, settings *conf.Settings) error {
	// Flags whose target the config load rewrites take their default from the loaded
	// settings rather than from viper, for the same reason as --locale in
	// cmd/root.go: cobra assigns the default into the bound variable at registration
	// time, so passing the raw viper value writes it straight back over whatever
	// load produced. For --source, --rtsp and --rtsptransport that would resurrect
	// the legacy keys MigrateAudioSourceConfig and MigrateRTSPConfig deliberately
	// cleared, and the next save would write them back into config.yaml. For
	// --listen it would undo the normalized telemetry address, leaving the endpoint
	// with nothing to bind.
	cmd.Flags().StringVar(&settings.Realtime.Audio.Export.Path, "clippath", viper.GetString("realtime.audio.export.path"), "Path to save audio clips")
	cmd.Flags().StringVar(&settings.Realtime.Audio.Source, "source", settings.Realtime.Audio.Source, "Audio capture source (\"sysdefault\", \"USB Audio\", \":0,0\", etc.)")
	cmd.Flags().StringVar(&settings.Realtime.Log.Path, "logpath", viper.GetString("realtime.log.path"), "Path to save log files")
	cmd.Flags().BoolVar(&settings.Realtime.ProcessingTime, "processingtime", viper.GetBool("realtime.processingtime"), "Report processing time for each detection")
	cmd.Flags().StringSliceVar(&settings.Realtime.RTSP.URLs, "rtsp", settings.Realtime.RTSP.URLs, "URL of RTSP audio stream to capture")
	cmd.Flags().StringVar(&settings.Realtime.RTSP.Transport, "rtsptransport", settings.Realtime.RTSP.Transport, "RTSP transport (tcp/udp)")
	cmd.Flags().BoolVar(&settings.Realtime.Telemetry.Enabled, "telemetry", viper.GetBool("realtime.telemetry.enabled"), "Enable Prometheus telemetry endpoint")
	cmd.Flags().StringVar(&settings.Realtime.Telemetry.Listen, "listen", settings.Realtime.Telemetry.Listen, "Listen address and port of telemetry endpoint")

	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("error binding flags: %w", err)
	}

	return nil
}

// emitStartupEvents records the startup event and checks for version changes.
// Version change detection is done BEFORE emitting the new startup event to
// avoid a race condition where the async persist of the new startup event
// could appear in the query results, requiring fragile skip logic.
func emitStartupEvents(ctx context.Context, settings *conf.Settings, ds datastore.Interface) {
	// Check for version change BEFORE emitting new startup (avoids race with async persist)
	recent, err := ds.GetRecentAppEvents(ctx, 50)
	if err == nil {
		for _, ev := range recent {
			if ev.Category == "system" && ev.EventType == "startup" {
				if prev, ok := ev.Metadata["version"].(string); ok && prev != "" && prev != settings.Version {
					events.Emit(ctx, "system", "version_change", "Application version changed", map[string]any{
						"previous_version": prev,
						"current_version":  settings.Version,
					})
				}
				break
			}
		}
	}

	events.Emit(ctx, "system", "startup", "Application started", map[string]any{
		"version":    settings.Version,
		"go_version": runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
	})
}
