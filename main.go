package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tphakala/go-birdnet/pkg/birdnet"
	"github.com/tphakala/go-birdnet/pkg/config"
	"github.com/tphakala/go-birdnet/pkg/myaudio"
	"github.com/tphakala/go-birdnet/pkg/observation"
)

func main() {
	// Load configuration
	config.Load()

	var cfg config.Settings

	rootCmd := &cobra.Command{
		Use:   "birdnet",
		Short: "Go-BirdNET CLI",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Normalize the input locale to lowercase
			inputLocale := strings.ToLower(cfg.Locale)

			// Check if input is already a locale code
			if _, exists := config.Locales[config.LocaleCodes[inputLocale]]; exists {
				cfg.Locale = inputLocale // The input is a valid locale code, so use it directly.
			} else {
				// If inputLocale is not a locale code, look for a full locale name match
				for code, fullName := range config.LocaleCodes {
					if strings.ToLower(fullName) == inputLocale {
						// Found full locale name, now get the corresponding locale code
						cfg.Locale = code
						break
					}
				}
			}

			// Now, cfg.Locale should be the locale code, check if it's in LocaleCodes map
			if fullLocale, exists := config.LocaleCodes[cfg.Locale]; !exists {
				return fmt.Errorf("unsupported locale: %s", cfg.Locale)
			} else {
				// Check if the corresponding label file exists
				if _, exists := config.Locales[fullLocale]; !exists {
					return fmt.Errorf("locale code supported but no label file found: %s", fullLocale)
				}
			}

			// At this point, cfg.Locale is set to a valid locale code
			// Proceed with the setup using the standardized locale code
			setupBirdNET(cfg)
			return nil
		},
	}

	// Set up the persistent debug flag for the root command.
	rootCmd.PersistentFlags().BoolVar(&cfg.Debug, "debug", viper.GetBool("debug"), "Enable debug output")
	//rootCmd.PersistentFlags().StringVar(&cfg.ModelPath, "model", viper.GetString("modelpath"), "Path to the model file")
	rootCmd.PersistentFlags().Float64Var(&cfg.Sensitivity, "sensitivity", viper.GetFloat64("sensitivity"), "Sigmoid sensitivity value between 0.0 and 1.5")
	rootCmd.PersistentFlags().StringVar(&cfg.Locale, "locale", viper.GetString("locale"), "Set the locale for labels. Accepts full name or 2-letter code.")

	fileCmd := setupFileCommand(&cfg)
	directoryCmd := setupDirectoryCommand(&cfg)
	realtimeCmd := setupRealtimeCommand(&cfg)

	rootCmd.AddCommand(fileCmd, realtimeCmd, directoryCmd)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func setupFileCommand(cfg *config.Settings) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "file [input.wav]",
		Short: "Analyze an audio file",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			cfg.InputFile = args[0]
			executeFileAnalysis(cfg)
		},
	}

	cmd.PersistentFlags().StringVar(&cfg.OutputDir, "output", viper.GetString("outputdir"), "Path to output directory")
	cmd.PersistentFlags().StringVar(&cfg.OutputType, "outputtype", viper.GetString("outputtype"), "Output type, defaults to table (Raven selection table)")
	cmd.PersistentFlags().Float64Var(&cfg.Overlap, "overlap", viper.GetFloat64("overlap"), "Overlap value between 0.0 and 2.9")

	return cmd
}

func setupDirectoryCommand(cfg *config.Settings) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "directory [path]",
		Short: "Analyze all *.wav files in a directory",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			cfg.InputDirectory = args[0]
			executeDirectoryAnalysis(cfg)
		},
	}

	return cmd
}

func setupRealtimeCommand(cfg *config.Settings) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "realtime",
		Short: "Analyze audio in realtime mode",
		Run:   func(cmd *cobra.Command, args []string) { executeRealtimeAnalysis(cfg) },
	}

	cmd.PersistentFlags().StringVar(&cfg.CapturePath, "savepath", viper.GetString("savepath"), "Path to save audio data to")
	cmd.PersistentFlags().StringVar(&cfg.LogPath, "logpath", viper.GetString("logpath"), "Path to save log file to")
	cmd.PersistentFlags().StringVar(&cfg.LogFile, "logfile", viper.GetString("logfile"), "Filename of logfile")
	cmd.PersistentFlags().Float64Var(&cfg.Threshold, "threshold", viper.GetFloat64("threshold"), "Threshold for detections")
	cmd.PersistentFlags().BoolVar(&cfg.ProcessingTime, "processingtime", viper.GetBool("processingtime"), "Report processing time for each detection")

	return cmd
}

// setupBirdNET initializes and loads the BirdNET model.
func setupBirdNET(cfg config.Settings) error {
	fmt.Println("Loading BirdNET model")
	if err := birdnet.InitializeModelFromEmbeddedData(); err != nil {
		log.Fatalf("failed to initialize model: %v", err)
	}

	if err := birdnet.LoadLabels(cfg.Locale); err != nil {
		log.Fatalf("failed to load labels: %v", err)
	}

	return nil
}

func executeFileAnalysis(cfg *config.Settings) {
	// Load and analyze the input audio file.
	audioData, err := myaudio.ReadAudioFile(cfg)
	if err != nil {
		log.Fatalf("error while reading input audio: %v", err)
	}

	notes, err := birdnet.AnalyzeAudio(audioData, cfg)
	if err != nil {
		log.Fatalf("eailed to analyze audio data: %v", err)
	}

	fmt.Println() // Empty line for better readability.
	// Print the detections (notes) with a threshold of, for example, 10%
	//output.PrintNotesWithThreshold(notes, 0.1)
	var outputFile string = ""
	if cfg.OutputDir != "" {
		outputFile = filepath.Join(cfg.OutputDir, filepath.Base(cfg.InputFile))
	}
	if cfg.OutputType == "" || cfg.OutputType == "table" {
		if outputFile != "" {
			outputFile = filepath.Join(outputFile + ".txt")
		}
		observation.WriteNotesTable(notes, outputFile)
	}
	if cfg.OutputType == "csv" {
		if outputFile != "" {
			outputFile = filepath.Join(outputFile + ".csv")
		}
		observation.WriteNotesCsv(notes, outputFile)
	}
}

// executeDirectoryAnalysis processes all .wav files in the given directory for analysis.
func executeDirectoryAnalysis(cfg *config.Settings) {
	// Read the list of files from the input directory.
	files, err := os.ReadDir(cfg.InputDirectory)
	if err != nil {
		log.Fatalf("Failed to open directory: %v", err)
	}

	for _, file := range files {
		// Check if the file has a .wav extension.
		if filepath.Ext(file.Name()) == ".wav" {
			inputFilePath := filepath.Join(cfg.InputDirectory, file.Name())
			fmt.Println("Analyzing file:", inputFilePath)
			// Create a copy of the config struct and set the input audio file path.
			//cfgCopy := *cfg
			cfg.InputFile = inputFilePath
			executeFileAnalysis(cfg)
		}
	}
}

// executeRealtimeAnalysis initiates the BirdNET Analyzer in real-time mode and waits for a termination signal.
func executeRealtimeAnalysis(cfg *config.Settings) {
	fmt.Println("Starting BirdNET Analyzer in realtime mode")

	// Start necessary routines for real-time analysis.
	myaudio.StartGoRoutines(cfg)

	// Channel to receive OS signals.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// Block until a signal is received.
	<-c

	// Close the QuitChannel to signal termination.
	close(myaudio.QuitChannel)
}
