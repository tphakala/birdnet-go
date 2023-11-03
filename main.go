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

// setupFileCommand creates and returns a Cobra command for the audio file analysis.
// This command is intended for analyzing a single audio file as specified by the user.
func setupFileCommand(cfg *config.Settings) *cobra.Command {
	// Define a new command with usage, short description, and required arguments.
	var cmd = &cobra.Command{
		Use:   "file [input.wav]",
		Short: "Analyze an audio file",
		Args:  cobra.ExactArgs(1), // Ensure exactly one argument is provided, the input file.

		// Run is the command's execution logic when the "file" command is invoked.
		Run: func(cmd *cobra.Command, args []string) {
			// Update the configuration to use the provided input file.
			cfg.InputFile = args[0]
			// Call the function to execute the file analysis with the updated configuration.
			executeFileAnalysis(cfg)
		},
	}

	// Setup persistent flags for the command which will be available to this command and all child commands.
	cmd.PersistentFlags().StringVar(&cfg.OutputDir, "output", viper.GetString("outputdir"), "Path to output directory")
	cmd.PersistentFlags().StringVar(&cfg.OutputType, "outputtype", viper.GetString("outputtype"), "Output type, defaults to table (Raven selection table)")
	cmd.PersistentFlags().Float64Var(&cfg.Overlap, "overlap", viper.GetFloat64("overlap"), "Overlap value between 0.0 and 2.9")

	// Return the fully configured command to be added to the root command of the CLI.
	return cmd
}

// setupDirectoryCommand initializes and returns a new cobra.Command for directory analysis.
// It configures the command to accept a directory path as an argument and sets up
// persistent flags that control the output and analysis behavior based on user input.
func setupDirectoryCommand(cfg *config.Settings) *cobra.Command {
	// Define the directory command with usage, description, and argument validation.
	var cmd = &cobra.Command{
		Use:   "directory [path]",
		Short: "Analyze all *.wav files in a directory",
		Long: `Provide a directory path to analyze all *.wav files within it. 
		       The analysis will use the provided configuration settings.`,
		Args: cobra.ExactArgs(1), // Ensure exactly one argument is provided.

		// Command execution logic when the directory command is invoked.
		Run: func(cmd *cobra.Command, args []string) {
			// Set the input directory in the configuration to the provided argument.
			cfg.InputDirectory = args[0]

			// Call the function that executes the directory analysis with the given configuration.
			executeDirectoryAnalysis(cfg)
		},
	}

	// Setup persistent flags for the command which will be available to this command and all child commands.
	cmd.PersistentFlags().StringVar(&cfg.OutputDir, "output", viper.GetString("outputdir"), "Path to output directory")
	cmd.PersistentFlags().StringVar(&cfg.OutputType, "outputtype", viper.GetString("outputtype"), "Output type, defaults to table (Raven selection table)")
	cmd.PersistentFlags().Float64Var(&cfg.Overlap, "overlap", viper.GetFloat64("overlap"), "Overlap value between 0.0 and 2.9")

	// Return the fully configured command to the caller.
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
// It prints a loading message, initializes the model with embedded data,
// and loads the labels according to the provided locale in the config.
// It returns an error if any step in the initialization process fails.
func setupBirdNET(cfg config.Settings) error {
	fmt.Println("Loading BirdNET model...")

	// Initialize the BirdNET model from embedded data.
	if err := birdnet.InitializeModelFromEmbeddedData(); err != nil {
		// Return an error allowing the caller to handle it.
		return fmt.Errorf("failed to initialize model: %w", err)
	}

	// Load the labels for the BirdNET model based on the locale specified in the configuration.
	if err := birdnet.LoadLabels(cfg.Locale); err != nil {
		// Return an error allowing the caller to handle it.
		return fmt.Errorf("failed to load labels: %w", err)
	}

	// If everything was successful, return nil indicating no error occurred.
	return nil
}

// executeFileAnalysis conducts an analysis of an audio file and outputs the results.
// It reads an audio file, analyzes it for bird sounds, and prints the results based on the provided configuration.
func executeFileAnalysis(cfg *config.Settings) {
	// Load and analyze the input audio file.
	audioData, err := myaudio.ReadAudioFile(cfg)
	if err != nil {
		// Use log.Fatalf to log the error and exit the program.
		log.Fatalf("error while reading input audio: %v", err)
	}

	// Analyze the loaded audio data for bird sounds.
	notes, err := birdnet.AnalyzeAudio(audioData, cfg)
	if err != nil {
		// Corrected typo "eailed" to "failed" and used log.Fatalf to log the error and exit.
		log.Fatalf("failed to analyze audio data: %v", err)
	}

	// Print a newline for better readability in the output.
	fmt.Println()

	// Prepare the output file path if OutputDir is specified in the configuration.
	var outputFile string
	if cfg.OutputDir != "" {
		// Safely concatenate file paths using filepath.Join to avoid cross-platform issues.
		outputFile = filepath.Join(cfg.OutputDir, filepath.Base(cfg.InputFile))
	}

	// Output the notes based on the desired output type in the configuration.
	// If OutputType is not specified or if it's set to "table", output as a table format.
	if cfg.OutputType == "" || cfg.OutputType == "table" {
		if err := observation.WriteNotesTable(notes, outputFile); err != nil {
			log.Fatalf("failed to write notes table: %v", err)
		}
	}
	// If OutputType is set to "csv", output as CSV format.
	if cfg.OutputType == "csv" {
		if err := observation.WriteNotesCsv(notes, outputFile); err != nil {
			log.Fatalf("failed to write notes CSV: %v", err)
		}
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

	// Delete tflite interpreter
	birdnet.DeleteInterpreter()

	// Close the QuitChannel to signal termination
	close(myaudio.QuitChannel)
}
