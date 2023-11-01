package main

import (
	"archive/zip"
	"fmt"
	"io"
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
	"github.com/tphakala/go-birdnet/pkg/output"
)

const (
	DefaultModelPath = "model/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite"
	//LabelFilePath    = "model/labels_fi.txt"
)

func main() {
	// Load configuration
	config.Load()

	var cfg config.Settings

	rootCmd := &cobra.Command{
		Use:   "birdnet",
		Short: "Go-BirdNET CLI",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if _, exists := config.Locales[cfg.Locale]; !exists {
				if fullLocale, exists := config.LocaleCodes[strings.ToLower(cfg.Locale)]; exists {
					cfg.Locale = fullLocale
				} else {
					return fmt.Errorf("unsupported locale: %s", cfg.Locale)
				}
			}
			LabelFilePath := fmt.Sprintf("model/%s", config.Locales[cfg.Locale])

			// Check if label file exists on disk.
			if _, err := os.Stat(LabelFilePath); os.IsNotExist(err) {
				// Attempt to extract from ZIP.
				if err := extractLabelFileFromZip("model/labels_nm.zip", LabelFilePath); err != nil {
					return fmt.Errorf("error extracting label file: %v", err)
				}
			}
			setupBirdNET(cfg.ModelPath, LabelFilePath)
			return nil
		},
	}

	// Set up the persistent debug flag for the root command.
	rootCmd.PersistentFlags().BoolVar(&cfg.Debug, "debug", viper.GetBool("debug"), "Enable debug output")
	rootCmd.PersistentFlags().StringVar(&cfg.ModelPath, "model", viper.GetString("modelpath"), "Path to the model file")
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
func setupBirdNET(modelPath string, LabelFilePath string) error {
	fmt.Println("Loading BirdNET model")
	if err := birdnet.InitializeModel(modelPath); err != nil {
		log.Fatalf("failed to initialize model: %v", err)
	}

	if err := birdnet.LoadLabels(LabelFilePath); err != nil {
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
	output.PrintNotesWithThreshold(notes, 0.1)
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

// extractLabelFileFromZip extracts a specified label file from a zip archive
func extractLabelFileFromZip(zipPath, labelFilePath string) error {
	// Open the zip archive for reading
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Iterate through the files in the zip archive
	for _, zipFile := range r.File {
		// Check if the current file matches the desired label file
		if zipFile.Name == filepath.Base(labelFilePath) {
			// Open the zip file entry for reading
			rc, err := zipFile.Open()
			if err != nil {
				return err
			}

			// Create the output file
			outFile, err := os.Create(labelFilePath)
			if err != nil {
				rc.Close() // Make sure to close the zip file entry before returning
				return err
			}

			// Copy the content from the zip file entry to the output file
			_, err = io.Copy(outFile, rc)

			// Close the files after copying
			rc.Close()
			outFile.Close()

			return err
		}
	}

	// Return an error if the desired label file wasn't found in the zip archive
	return fmt.Errorf("label file not found in the zip archive")
}
